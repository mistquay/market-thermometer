// Package ext 是外部进程插件的宿主：插件是独立程序（任意语言），通过标准输入输出上按行分隔的
// JSON-RPC 2.0 通信（协议见 docs/plugin-protocol.md）。核心负责启动、超时、重启与隔离：
//   - 懒启动：第一次调用时启动进程，并发送 initialize（下发配置）；
//   - 每个进程同一时间只处理一个请求；调用超时即结束进程，下次调用时重启；
//   - 10 分钟内最多启动 5 次，超过后暂停调用，避免崩溃循环；
//   - 插件的 stderr 保留最近 50 行，供管理页查看；返回的数据一律按不可信输入校验。
//
// 可执行文件与参数只能来自 config.yaml（Web 不能新增或修改），等同于执行任意命令。
package ext

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"market-thermometer/internal/plugin"
)

// ProtocolVersion 是当前协议版本，随 initialize 发给插件。
const ProtocolVersion = 1

const (
	maxLine        = 32 << 20 // 单条消息上限 32MB
	stderrLines    = 50
	restartWindow  = 10 * time.Minute
	restartLimit   = 5
	defaultTimeout = 60 * time.Second
)

// Spec 是 config.yaml 中的一个外部插件。
type Spec struct {
	Dir     string        // 插件目录（含 plugin.json），也是进程的工作目录
	Command []string      // 可执行文件与参数
	Timeout time.Duration // 单次调用超时，0 为 60 秒
	Env     []string      // 额外环境变量（KEY=VALUE）
}

// Process 是一个外部插件进程（按需启动）。
type Process struct {
	spec     Spec
	manifest plugin.Manifest

	mu     sync.Mutex // 串行化调用
	config map[string]string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte   // 读协程送来的 stdout 行；关闭表示进程已退出
	done   chan struct{} // 进程被结束时关闭，让读协程退出
	nextID int64
	starts []time.Time

	infoMu sync.Mutex // 保护以下状态（管理页读取，不能被长时间调用阻塞）
	info   Status
	stderr []string
}

// Load 读取并校验插件目录下的 plugin.json（不启动进程）。
func Load(spec Spec) (*Process, error) {
	if len(spec.Command) == 0 {
		return nil, errors.New("插件未配置 command")
	}
	b, err := os.ReadFile(filepath.Join(spec.Dir, "plugin.json"))
	if err != nil {
		return nil, fmt.Errorf("读取插件清单：%w", err)
	}
	var m plugin.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("插件清单格式错误：%w", err)
	}
	m.Builtin = false
	if m.Label == "" {
		m.Label = m.Name
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("插件清单 %s：%w", spec.Dir, err)
	}
	if spec.Timeout <= 0 {
		spec.Timeout = defaultTimeout
	}
	return &Process{spec: spec, manifest: m}, nil
}

func (p *Process) Manifest() plugin.Manifest { return p.manifest }

// SetConfig 设置下发给插件的配置；进程已运行时下次调用前重启以生效。
func (p *Process) SetConfig(cfg map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = cfg
	p.stopLocked()
}

// rpcError 是插件返回的业务错误。
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("插件返回错误 %d：%s", e.Code, truncate(e.Message, 500))
}

// Call 调用插件方法，result 为 nil 时忽略返回值。业务错误（插件返回 error）不会导致重启。
func (p *Process) Call(ctx context.Context, method string, params, result any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	err := p.callLocked(ctx, method, params, result)
	var re *rpcError
	if err != nil && !errors.As(err, &re) {
		p.setInfo(func(s *Status) { s.LastError = err.Error() })
	}
	return err
}

func (p *Process) callLocked(ctx context.Context, method string, params, result any) error {
	if p.cmd == nil {
		if err := p.startLocked(ctx); err != nil {
			return err
		}
	}
	return p.roundTrip(ctx, method, params, result)
}

func (p *Process) startLocked(ctx context.Context) error {
	now := time.Now()
	recent := p.starts[:0]
	for _, t := range p.starts {
		if now.Sub(t) < restartWindow {
			recent = append(recent, t)
		}
	}
	p.starts = recent
	if len(p.starts) >= restartLimit {
		return fmt.Errorf("插件 %s 在 %v 内已启动 %d 次，暂停调用（检查插件日志）", p.manifest.Name, restartWindow, restartLimit)
	}
	p.starts = append(p.starts, now)

	cmd := exec.Command(p.spec.Command[0], p.spec.Command[1:]...)
	cmd.Dir = p.spec.Dir
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUNBUFFERED=1")
	cmd.Env = append(cmd.Env, p.spec.Env...)
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动插件 %s：%w", p.manifest.Name, err)
	}
	lines, done := make(chan []byte, 1), make(chan struct{})
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), maxLine)
		for sc.Scan() {
			select {
			case lines <- append([]byte(nil), sc.Bytes()...):
			case <-done:
				return
			}
		}
		if err := sc.Err(); err != nil {
			p.logStderr("[宿主] 读取插件输出失败：" + err.Error())
		}
	}()
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 4<<10), 1<<20)
		for sc.Scan() {
			p.logStderr(sc.Text())
		}
	}()
	go func() { _ = cmd.Wait() }()
	p.cmd, p.stdin, p.lines, p.done = cmd, stdin, lines, done
	p.setInfo(func(s *Status) { s.Running, s.PID, s.Starts = true, cmd.Process.Pid, len(p.starts) })
	init := map[string]any{"protocol": ProtocolVersion, "config": orEmpty(p.config)}
	if err := p.roundTrip(ctx, "initialize", init, nil); err != nil {
		p.stopLocked()
		return fmt.Errorf("插件 %s 初始化失败：%w", p.manifest.Name, err)
	}
	return nil
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// roundTrip 发送一个请求并等待对应的响应；超时、进程退出或协议错误时结束进程。
func (p *Process) roundTrip(ctx context.Context, method string, params, result any) error {
	p.nextID++
	id := p.nextID
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err := p.stdin.Write(append(req, '\n')); err != nil {
		p.stopLocked()
		return fmt.Errorf("写入插件失败（进程可能已退出）：%w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, p.spec.Timeout)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			p.stopLocked()
			return fmt.Errorf("调用 %s 超时或取消（%v），已结束插件进程：%w", method, p.spec.Timeout, ctx.Err())
		case line, ok := <-p.lines:
			if !ok {
				p.stopLocked()
				return fmt.Errorf("插件进程在调用 %s 时退出", method)
			}
			var resp struct {
				ID     *int64          `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *rpcError       `json:"error"`
			}
			if err := json.Unmarshal(line, &resp); err != nil || resp.ID == nil {
				p.logStderr("[宿主] 忽略非协议输出：" + truncate(string(line), 200)) // 插件误把日志打到了 stdout
				continue
			}
			if *resp.ID != id {
				p.stopLocked()
				return fmt.Errorf("响应 id %d 与请求 %d 不匹配，已结束插件进程", *resp.ID, id)
			}
			if resp.Error != nil {
				return resp.Error
			}
			if result != nil {
				if err := json.Unmarshal(resp.Result, result); err != nil {
					return fmt.Errorf("%s 返回格式错误：%w", method, err)
				}
			}
			return nil
		}
	}
}

func (p *Process) stopLocked() {
	if p.cmd == nil {
		return
	}
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	close(p.done)
	p.cmd, p.stdin, p.lines, p.done = nil, nil, nil, nil
	p.setInfo(func(s *Status) { s.Running, s.PID = false, 0 })
}

func (p *Process) setInfo(f func(*Status)) {
	p.infoMu.Lock()
	defer p.infoMu.Unlock()
	f(&p.info)
}

// Close 结束插件进程（先礼貌地发送 shutdown）。
func (p *Process) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = p.roundTrip(ctx, "shutdown", nil, nil)
		cancel()
	}
	p.stopLocked()
}

func (p *Process) logStderr(line string) {
	p.infoMu.Lock()
	defer p.infoMu.Unlock()
	p.stderr = append(p.stderr, truncate(line, 500))
	if len(p.stderr) > stderrLines {
		p.stderr = p.stderr[len(p.stderr)-stderrLines:]
	}
}

// Status 是插件进程的运行状态。
type Status struct {
	Running   bool     `json:"running"`
	PID       int      `json:"pid,omitempty"`
	Starts    int      `json:"starts"` // 最近 10 分钟内的启动次数
	LastError string   `json:"lastError,omitempty"`
	Stderr    []string `json:"stderr"`
	Command   []string `json:"command"`
	Dir       string   `json:"dir"`
}

func (p *Process) Status() Status {
	p.infoMu.Lock()
	defer p.infoMu.Unlock()
	st := p.info
	st.Command, st.Dir = p.spec.Command, p.spec.Dir
	st.Stderr = append([]string{}, p.stderr...)
	return st
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
