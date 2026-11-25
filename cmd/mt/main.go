// mt 市场波段温度计。
//
//	mt serve  [-c config.yaml] [-trust-proxy] [-schedule=false]  启动 Web 服务与定时任务（主要使用方式）
//	mt sync   [-c config.yaml] [-full]   命令行同步数据（调试/运维用）
//	mt breadth [-c config.yaml] [-full]  计算市场广度（-full 清空后从头回填）
//	mt score  [-c config.yaml]           按生效参数重算全部得分
//	mt import-bars [-c config.yaml] [-source 名称] 文件.csv  导入日线（code,date,close[,mult]），同日覆盖
//	mt gapfill [-c config.yaml] -source tencent 代码…  用数据源补齐个股日线缺口（不覆盖已有数据）
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
	"path/filepath"
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
	_ "market-thermometer/internal/derived/ma200"
	"market-thermometer/internal/importer"
	"market-thermometer/internal/job"
	"market-thermometer/internal/market"
	"market-thermometer/internal/notify"
	_ "market-thermometer/internal/notify/dingtalk"
	_ "market-thermometer/internal/notify/feishu"
	_ "market-thermometer/internal/notify/telegram"
	_ "market-thermometer/internal/notify/wecom"
	"market-thermometer/internal/plugin"
	"market-thermometer/internal/plugin/ext"
	"market-thermometer/internal/store"
	_ "market-thermometer/internal/strategy/thermometer"
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
	case "import-bars":
		err = cmdImportBars(os.Args[2:])
	case "gapfill":
		err = cmdGapFill(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "用法: mt serve|sync|breadth|score|gapfill|import-bars|hashpw [-c config.yaml]")
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
	enabled := cfg.Sources.Enabled
	for _, pc := range cfg.Plugins { // 外部插件须在构建 Hub 之前登记（清单中的新指标在此定义）
		if pc.Disabled {
			continue
		}
		dir, _ := filepath.Abs(pc.Dir)
		p, err := ext.Install(ext.Spec{Dir: dir, Command: pc.Command, Timeout: time.Duration(pc.TimeoutS) * time.Second})
		if err != nil {
			log.Printf("外部插件 %s 加载失败（已跳过）：%v", pc.Dir, err)
			continue
		}
		p.SetConfig(pc.Config)
		m := p.Manifest()
		if saved, ok, err := st.PluginConfig(context.Background(), m.ID()); err == nil && ok { // Web 上保存的设置优先
			p.SetConfig(saved.Config)
			p.SetEnabled(saved.Enabled)
		}
		if m.Kind == plugin.KindMetric && len(enabled) > 0 {
			enabled = append(enabled, m.Name)
		}
		log.Printf("外部插件 %s（%s %s）已登记", m.Name, plugin.KindLabel[m.Kind], m.Version)
	}
	// 数据源默认直连（A股接口在大陆直连最稳）；http_proxy 只给需要翻墙的出站请求用。
	env := ds.Env{HTTP: &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: nil}}, Now: time.Now}
	hub, err := ds.NewHub(env, enabled, cfg.Sources.Routes, cfg.Sources.Options)
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
			runner.SetSkipFunds(cfg.Stocks.SkipFunds)
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
	schedule := fs.Bool("schedule", true, "启用每日定时同步与推送（交易日 17:30 起，22:00 截止）")
	a, err := setup(fs, args)
	if err != nil {
		return err
	}
	defer a.store.Close()
	defer ext.CloseAll()
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
			PluginOptions: builtinOptions(a.cfg),
			Pusher:        pusher,
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
	if *schedule {
		go job.NewScheduler(a.runner, pusher).Run(ctx)
		log.Printf("定时任务已启用：交易日 17:30 同步，不完整时每 30 分钟重试，22:00 截止")
	}
	log.Printf("listening on http://%s  数据源: %v", a.cfg.Listen, a.hub.Sources())
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// builtinOptions 汇总内置插件在 config.yaml 中的选项（插件管理页只读展示）。
func builtinOptions(cfg *config.Config) map[string]map[string]any {
	out := map[string]map[string]any{}
	for name, o := range cfg.Sources.Options {
		out[string(plugin.KindMetric)+"/"+name] = o
	}
	for name, o := range cfg.Stocks.Options {
		out[string(plugin.KindStock)+"/"+name] = o
	}
	return out
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
	local := fs.Bool("local", false, "不同步个股日线，只从本地数据重算派生指标（市场广度）")
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
	run := func() (*job.Status, error) { return a.runner.RunBreadth(ctx, *full) }
	if *local {
		run = func() (*job.Status, error) { return a.runner.RunDerivedLocal(ctx) }
	}
	st, err := run()
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

func cmdImportBars(args []string) error {
	fs := flag.NewFlagSet("import-bars", flag.ExitOnError)
	source := fs.String("source", "import", "写入 stock_bar.source 的来源名")
	a, err := setup(fs, args)
	if err != nil {
		return err
	}
	defer a.store.Close()
	if fs.NArg() != 1 {
		return errors.New("用法：mt import-bars [-source 名称] 文件.csv（列：code,date,close[,mult]）")
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	bars, err := importer.ParseBars(f)
	if err != nil {
		return err
	}
	res, err := importer.ImportBars(context.Background(), a.store, bars, *source, time.Now())
	if err != nil {
		return err
	}
	fmt.Println(fmt.Sprintf("已导入 %d 只证券、%d 根日线（来源 %s）。", res.Codes, res.Rows, *source),
		"含股票时运行 mt breadth -local 重算市场广度。")
	return nil
}

func cmdGapFill(args []string) error {
	fs := flag.NewFlagSet("gapfill", flag.ExitOnError)
	source := fs.String("source", "tencent", "用于补缺口的个股数据源（须在 stocks.sources 中）")
	a, err := setup(fs, args)
	if err != nil {
		return err
	}
	defer a.store.Close()
	if _, err := a.runner.StartGapFill(*source, fs.Args()); err != nil {
		return err
	}
	printed := 0
	for {
		time.Sleep(time.Second)
		st := a.runner.Current()
		for ; printed < len(st.Log); printed++ {
			fmt.Println(st.Log[printed])
		}
		if st.State != "running" {
			if st.State != "done" {
				return fmt.Errorf("结束状态：%s", st.State)
			}
			return nil
		}
	}
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
