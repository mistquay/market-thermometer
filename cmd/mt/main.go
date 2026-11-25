// mt 市场波段温度计。
//
//	mt serve  [-c config.yaml]           启动 Web 服务（主要使用方式）
//	mt sync   [-c config.yaml] [-full]   命令行同步数据（调试/运维用）
//	mt breadth [-c config.yaml] [-full]  计算市场广度（-full 清空后从头回填）
//	mt score  [-c config.yaml]           按生效参数重算全部得分
//	mt hashpw                            从标准输入读口令，输出 bcrypt 哈希，填入 config.yaml
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"market-thermometer/internal/config"
	ds "market-thermometer/internal/datasource"
	_ "market-thermometer/internal/datasource/csindex"
	_ "market-thermometer/internal/datasource/eastmoney"
	_ "market-thermometer/internal/datasource/stockdb"
	_ "market-thermometer/internal/datasource/tencent"
	"market-thermometer/internal/job"
	"market-thermometer/internal/market"
	"market-thermometer/internal/notify"
	_ "market-thermometer/internal/notify/dingtalk"
	_ "market-thermometer/internal/notify/feishu"
	_ "market-thermometer/internal/notify/telegram"
	_ "market-thermometer/internal/notify/wecom"
	"market-thermometer/internal/store"
	"market-thermometer/internal/web"
	"market-thermometer/internal/webui"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "sync":
		err = cmdSync(os.Args[2:])
	case "breadth":
		err = cmdBreadth(os.Args[2:])
	case "score":
		err = cmdScore(os.Args[2:])
	case "hashpw":
		err = cmdHashPW()
	default:
		usage()
	}
	if err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法: mt serve|sync|breadth|score|hashpw [-c config.yaml]")
	os.Exit(2)
}

type app struct {
	cfg    *config.Config
	store  *store.Store
	hub    *ds.Hub
	runner *job.Runner
}

func setup(fs *flag.FlagSet, args []string) (*app, error) {
	cfgPath := fs.String("c", "config.yaml", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	// 数据源默认直连（A股接口在大陆直连最稳）；http_proxy 只给需要翻墙的出站请求用。
	env := ds.Env{HTTP: &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: nil}}, Now: time.Now}
	hub, err := ds.NewHub(env, cfg.Sources.Enabled, cfg.Sources.Routes, cfg.Sources.Options)
	if err != nil {
		st.Close()
		return nil, err
	}
	runner := job.NewRunner(st, hub, market.CN, cfg.BackfillStartTime())
	built := map[string]ds.StockSource{}
	buildList := func(names []string) ([]ds.StockSource, error) {
		var out []ds.StockSource
		for _, name := range names {
			if src, ok := built[name]; ok {
				out = append(out, src)
				continue
			}
			e := env
			e.Options = cfg.Stocks.Options[name]
			e.HTTP = &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{Proxy: nil}}
			src, err := ds.BuildStock(name, e)
			if err != nil {
				return nil, err
			}
			built[name] = src
			out = append(out, src)
		}
		return out, nil
	}
	stockSrcs, err := buildList(cfg.Stocks.SourceList())
	if err == nil {
		var fundSrcs []ds.StockSource
		if fundSrcs, err = buildList(cfg.Stocks.FundSources); err == nil {
			runner.SetStocks(stockSrcs, cfg.Stocks.Concurrency)
			runner.SetFundSources(fundSrcs)
		}
	}
	if err != nil {
		st.Close()
		return nil, err
	}
	var fundSrcs []ds.FundSource
	for _, name := range cfg.Funds.Sources {
		src, err := ds.BuildFund(name, env)
		if err != nil {
			st.Close()
			return nil, err
		}
		fundSrcs = append(fundSrcs, src)
	}
	runner.SetFundNAVSources(fundSrcs)
	return &app{cfg: cfg, store: st, hub: hub, runner: runner}, nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	trustProxy := fs.Bool("trust-proxy", false, "信任反向代理的 X-Real-IP 头（部署在 Caddy/Nginx 之后时开启）")
	a, err := setup(fs, args)
	if err != nil {
		return err
	}
	defer a.store.Close()
	if a.cfg.AdminPasswordHash == "" {
		return errors.New("config.yaml 未设置 admin_password_hash：先运行 `mt hashpw` 生成")
	}
	if err := a.store.FailStaleJobs(context.Background(), time.Now()); err != nil {
		return err
	}
	// 启动时按生效参数重算一次，保证参数变更或升级后得分立即可用
	if res, err := job.Score(context.Background(), a.store, a.hub, market.CN, time.Now()); err != nil {
		log.Printf("打分失败（先同步数据）：%v", err)
	} else {
		log.Printf("打分完成：参数 %s，%d 个交易日，最新 %s", res.Version, res.Days, res.Last.Date)
	}
	pusher, err := a.newPusher()
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: a.cfg.Listen,
		Handler: web.New(web.Options{
			PasswordHash: a.cfg.AdminPasswordHash, TrustProxy: *trustProxy, Static: webui.FS(), StockSource: strings.Join(a.cfg.Stocks.SourceList(), " → "),
			Pusher: pusher,
		}, a.store, a.hub, a.runner).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	log.Printf("listening on http://%s  数据源: %v", a.cfg.Listen, a.hub.Sources())
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// newPusher 构造推送：渠道默认直连；http_proxy 只给需要翻墙的渠道（Telegram）用。
func (a *app) newPusher() (*job.Pusher, error) {
	direct := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}}
	proxied := direct
	if a.cfg.HTTPProxy != "" {
		u, err := url.Parse(a.cfg.HTTPProxy)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("http_proxy 格式不对：%q", a.cfg.HTTPProxy)
		}
		proxied = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
	}
	return job.NewPusher(a.store, notify.Env{HTTP: direct, Proxy: proxied}, market.CN), nil
}

func cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	full := fs.Bool("full", false, "从 backfill_start 全量回填")
	a, err := setup(fs, args)
	if err != nil {
		return err
	}
	defer a.store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	st, err := a.runner.RunSync(ctx, *full)
	if err != nil {
		return err
	}
	for _, line := range st.Log {
		fmt.Println(line)
	}
	if st.State != "done" {
		return fmt.Errorf("同步结束状态：%s", st.State)
	}
	return nil
}

func cmdBreadth(args []string) error {
	fs := flag.NewFlagSet("breadth", flag.ExitOnError)
	full := fs.Bool("full", false, "清空后从头回填")
	a, err := setup(fs, args)
	if err != nil {
		return err
	}
	defer a.store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// 实时打印进度日志
	done := make(chan struct{})
	go func() {
		printed := 0
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if st := a.runner.Current(); st != nil {
					for ; printed < len(st.Log); printed++ {
						fmt.Println(st.Log[printed])
					}
				}
			}
		}
	}()
	st, err := a.runner.RunBreadth(ctx, *full)
	close(done)
	if err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	for _, line := range st.Log[max(0, len(st.Log)-3):] {
		fmt.Println(line)
	}
	if st.State != "done" {
		return fmt.Errorf("结束状态：%s", st.State)
	}
	return nil
}

func cmdScore(args []string) error {
	a, err := setup(flag.NewFlagSet("score", flag.ExitOnError), args)
	if err != nil {
		return err
	}
	defer a.store.Close()
	start := time.Now()
	res, err := job.Score(context.Background(), a.store, a.hub, market.CN, time.Now())
	if err != nil {
		return err
	}
	d := res.Last
	fmt.Printf("参数 %s，%d 个交易日，耗时 %v；数据源 %v\n", res.Version, res.Days, time.Since(start).Round(time.Millisecond), res.Sources)
	fmt.Printf("最新 %s：收盘 %.2f  V=%.1f(%s)  T1=%.1f T2=%.1f T4=%.1f  T_base=%.1f T_pct=%.1f T_s=%.1f  降级=%v\n",
		d.Date, d.Close, d.V, d.Tier, d.T[0], d.T[1], d.T[3], d.TBase, d.TPct, d.TS, d.Degraded)
	return nil
}

func cmdHashPW() error {
	fmt.Fprint(os.Stderr, "输入管理员口令（至少 8 位）：")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return err
	}
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < 8 {
		return errors.New("口令至少 8 位")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	fmt.Printf("admin_password_hash: %q\n", string(h))
	return nil
}
