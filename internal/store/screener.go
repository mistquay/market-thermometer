package store

import (
	"context"
	"database/sql"
	"math"
)

// ScreenerRow 是本地 A 股 / 场内基金选股器的一只证券最新快照指标（全本地计算，零外部抓取）。
type ScreenerRow struct {
	Code       string   `json:"code"`
	Kind       string   `json:"kind"` // stock | fund
	Name       string   `json:"name"`
	Date       string   `json:"date"`
	Close      float64  `json:"close"`
	Ret20      *float64 `json:"ret20"`      // 近 20 交易日复权涨跌幅
	Ret60      *float64 `json:"ret60"`      // 近 60 交易日复权涨跌幅
	AboveMA200 *bool    `json:"aboveMa200"` // 复权收盘价是否站上 MA200
	DistMA200  *float64 `json:"distMa200"`  // 相对 MA200 偏离度（小数）
	PETTM      *float64 `json:"peTtm"`
	PB         *float64 `json:"pb"`
	PEPct5Y    *float64 `json:"pePct5Y"` // 近 5 年 PE 历史分位（0–100）
	PBPct5Y    *float64 `json:"pbPct5Y"` // 近 5 年 PB 历史分位（0–100）
	TotalMV    *float64 `json:"totalMv"` // 总市值（亿元）
	Amount     *float64 `json:"amount"`  // 当日成交额（万元）
	Watched    bool     `json:"watched,omitempty"`
}

// BuildScreenerSnapshot 扫描本地在市证券（股票与场内基金）的最新行情、均线位置与扩展估值字段。
// 利用 stock_bar 与 stock_daily_ext 的 (code, d) 主键倒序取最新 205 根日线，首次构建约 1–3 秒，上层按最新日期缓存。
func (s *Store) BuildScreenerSnapshot(ctx context.Context) ([]ScreenerRow, string, error) {
	var maxD sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(last_date) FROM stock_list WHERE delisted=0 AND last_date IS NOT NULL`).Scan(&maxD); err != nil {
		return nil, "", err
	}
	if !maxD.Valid || maxD.Int64 == 0 {
		return []ScreenerRow{}, "", nil
	}
	asOf := intDate(int(maxD.Int64))

	rows, err := s.db.QueryContext(ctx, `SELECT l.code, l.kind, COALESCE(n.name, ''), l.last_date
		FROM stock_list l LEFT JOIN security_name n ON n.code=l.code
		WHERE l.delisted=0 AND l.last_date IS NOT NULL ORDER BY l.code`)
	if err != nil {
		return nil, "", err
	}
	type meta struct {
		code, kind, name string
		lastD            int
	}
	var metas []meta
	for rows.Next() {
		var m meta
		if err := rows.Scan(&m.code, &m.kind, &m.name, &m.lastD); err != nil {
			rows.Close()
			return nil, "", err
		}
		metas = append(metas, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	barStmt, err := s.db.PrepareContext(ctx, `SELECT close, mult FROM stock_bar WHERE code=? ORDER BY d DESC LIMIT 205`)
	if err != nil {
		return nil, "", err
	}
	defer barStmt.Close()
	amtStmt, err := s.db.PrepareContext(ctx, `SELECT amount FROM stock_daily_ext WHERE code=? AND d<=? AND amount>0 ORDER BY d DESC LIMIT 1`)
	if err != nil {
		return nil, "", err
	}
	defer amtStmt.Close()
	valStmt, err := s.db.PrepareContext(ctx, `SELECT pe_ttm, pb, total_mv FROM stock_daily_ext WHERE code=? AND d<=? AND (pe_ttm IS NOT NULL OR pb IS NOT NULL OR total_mv IS NOT NULL) ORDER BY d DESC LIMIT 1`)
	if err != nil {
		return nil, "", err
	}
	defer valStmt.Close()
	pctStmt, err := s.db.PrepareContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN pe_ttm>0 AND pe_ttm<=? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN pe_ttm>0 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN pb>0 AND pb<=? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN pb>0 THEN 1 ELSE 0 END), 0)
		FROM stock_daily_ext WHERE code=? AND d>=? AND d<=?`)
	if err != nil {
		return nil, "", err
	}
	defer pctStmt.Close()

	out := make([]ScreenerRow, 0, len(metas))
	type barPt struct{ c, m float64 }
	buf := make([]barPt, 0, 205)

	for _, m := range metas {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		r, err := barStmt.QueryContext(ctx, m.code)
		if err != nil {
			return nil, "", err
		}
		buf = buf[:0]
		for r.Next() {
			var bp barPt
			if err := r.Scan(&bp.c, &bp.m); err != nil {
				r.Close()
				return nil, "", err
			}
			buf = append(buf, bp)
		}
		r.Close()
		if len(buf) == 0 {
			continue
		}
		// 倒序 → 正序，链式计算后复权价
		n := len(buf)
		hfq := make([]float64, n)
		for i := 0; i < n; i++ {
			b := buf[n-1-i]
			if i == 0 {
				hfq[i] = b.c
			} else {
				prevC := buf[n-i].c
				if prevC > 0 && b.m > 0 {
					hfq[i] = hfq[i-1] * b.c * b.m / prevC
				} else {
					hfq[i] = hfq[i-1]
				}
			}
		}
		row := ScreenerRow{Code: m.code, Kind: m.kind, Name: m.name, Date: intDate(m.lastD), Close: buf[0].c}
		lastH := hfq[n-1]
		if n > 20 && hfq[n-1-20] > 0 {
			v := round4(lastH/hfq[n-1-20] - 1)
			row.Ret20 = &v
		}
		if n > 60 && hfq[n-1-60] > 0 {
			v := round4(lastH/hfq[n-1-60] - 1)
			row.Ret60 = &v
		}
		if n >= 200 {
			sum := 0.0
			for i := n - 200; i < n; i++ {
				sum += hfq[i]
			}
			ma200 := sum / 200
			if ma200 > 0 {
				ab := lastH > ma200
				dist := round4(lastH/ma200 - 1)
				row.AboveMA200 = &ab
				row.DistMA200 = &dist
			}
		}
		var amt sql.NullFloat64
		if err := amtStmt.QueryRowContext(ctx, m.code, m.lastD).Scan(&amt); err == nil && amt.Valid && amt.Float64 > 0 {
			v := math.Round(amt.Float64/1e4*10) / 10 // 元 → 万元
			row.Amount = &v
		}
		var pe, pb, tmv sql.NullFloat64
		if err := valStmt.QueryRowContext(ctx, m.code, m.lastD).Scan(&pe, &pb, &tmv); err == nil {
			if pe.Valid {
				v := math.Round(pe.Float64*100) / 100
				row.PETTM = &v
			}
			if pb.Valid {
				v := math.Round(pb.Float64*100) / 100
				row.PB = &v
			}
			if tmv.Valid && tmv.Float64 > 0 {
				v := math.Round(tmv.Float64/1e8*100) / 100 // 元 → 亿元
				row.TotalMV = &v
			}
			if (pe.Valid && pe.Float64 > 0) || (pb.Valid && pb.Float64 > 0) {
				var peLE, peN, pbLE, pbN int
				cutoff5y := m.lastD - 50000
				if err := pctStmt.QueryRowContext(ctx, pe.Float64, pb.Float64, m.code, cutoff5y, m.lastD).Scan(&peLE, &peN, &pbLE, &pbN); err == nil {
					if pe.Valid && pe.Float64 > 0 && peN >= 60 {
						p := math.Round(float64(peLE)/float64(peN)*1000) / 10
						row.PEPct5Y = &p
					}
					if pb.Valid && pb.Float64 > 0 && pbN >= 60 {
						p := math.Round(float64(pbLE)/float64(pbN)*1000) / 10
						row.PBPct5Y = &p
					}
				}
			}
		}
		out = append(out, row)
	}
	return out, asOf, nil
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }
