package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"time"

	"market-thermometer/internal/config"
	"market-thermometer/internal/engine"
	"market-thermometer/internal/job"
	"market-thermometer/internal/market"
	"market-thermometer/internal/store"
)

// cmdConfig 提供数据库配置的命令行查看、YAML 导入与校准参数激活工具。
//
//	mt config show [-db data/mt.db]
//	mt config import [-db data/mt.db] [-file config.yaml]
//	mt config apply-recommended [-db data/mt.db]
func cmdConfig(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: mt config show | import | apply-recommended")
	}
	sub := args[0]
	fs := flag.NewFlagSet("config "+sub, flag.ExitOnError)
	switch sub {
	case "show":
		dbPath := fs.String("db", defaultDBPath(), "SQLite 数据库路径")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		st, err := store.Open(*dbPath)
		if err != nil {
			return err
		}
		defer st.Close()
		cfg, src, updated, ok, err := st.LoadAppConfig(context.Background())
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("数据库尚无 app_config 记录（启动 mt serve 时将自动初始化内置默认配置）。")
			return nil
		}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Printf("# source=%s updatedAt=%s hasPassword=%v\n%s\n", src, updated, cfg.AdminPasswordHash != "", string(b))
		return nil

	case "import":
		dbPath := fs.String("db", defaultDBPath(), "SQLite 数据库路径")
		file := fs.String("file", "config.yaml", "要导入的 YAML 配置文件")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cfg, err := config.Load(*file)
		if err != nil {
			return err
		}
		st, err := store.Open(*dbPath)
		if err != nil {
			return err
		}
		defer st.Close()
		now := time.Now()
		if err := st.SaveAppConfig(context.Background(), cfg, "imported", now); err != nil {
			return err
		}
		if cfg.AdminPasswordHash != "" {
			if err := st.SaveAdminPasswordHash(context.Background(), cfg.AdminPasswordHash, now); err != nil {
				return err
			}
		}
		fmt.Printf("已导入 %s → %s（app_config）；后续可直接运行 `mt serve` 零配置启动。\n", *file, *dbPath)
		return nil

	case "apply-recommended":
		// 启用 Polymarket WS + Student-t(df=6) 并激活 A 股「方案C + 仅估值昂贵时卖顶」参数，重算得分与信号
		a, err := setup(fs, args[1:])
		if err != nil {
			return err
		}
		defer a.store.Close()
		ctx := context.Background()
		now := time.Now()
		a.cfg.Polymarket.Book.WS = true
		a.cfg.Polymarket.Strategy.Shadow = true
		if a.cfg.Polymarket.Strategy.DF <= 2 {
			a.cfg.Polymarket.Strategy.DF = 6
		}
		if err := a.store.SaveAppConfig(ctx, a.cfg, "db", now); err != nil {
			return err
		}
		_ = a.store.AddAudit(ctx, "cli", "config.save", "app_config", map[string]any{
			"pmWS": a.cfg.Polymarket.Book.WS, "pmDF": a.cfg.Polymarket.Strategy.DF, "note": "apply-recommended",
		}, now)

		cal := engine.CalibratedParams()
		if err := a.store.SaveParams(ctx, "方案C+昂贵卖顶（已校准）", cal, true, now); err != nil {
			return err
		}
		_ = a.store.AddAudit(ctx, "cli", "params.save", cal.Version(), map[string]any{
			"name": "方案C+昂贵卖顶（已校准）", "activate": true, "skipTPct": cal.SkipTPct, "topOnlyExpensive": cal.Signal.TopOnlyExpensive,
		}, now)
		res, err := job.Score(ctx, a.store, a.hub, market.CN, now)
		if err != nil {
			return fmt.Errorf("参数已激活，但重算得分失败: %w", err)
		}
		fmt.Printf("✓ 已启用 Polymarket WS=%v, Student-t df=%.0f（已落库 app_config）\n", a.cfg.Polymarket.Book.WS, a.cfg.Polymarket.Strategy.DF)
		fmt.Printf("✓ 已激活 A 股温度计校准参数「方案C+昂贵卖顶（已校准）」(%s)，重算 %d 个交易日，最新 %s（T_s=%.1f, V=%.1f %s）\n",
			res.Version, res.Days, res.Last.Date, res.Last.TS, res.Last.V, res.Last.Tier)
		return nil

	default:
		return fmt.Errorf("未知子命令 %q（支持 show | import | apply-recommended）", sub)
	}
}
