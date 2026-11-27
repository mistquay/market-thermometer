package web

import (
	"context"
	"math"
	"net/http"
	"sync"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/datasource/csindex"
)

type radarDef struct {
	Code  string
	Name  string
	Style string
	IsTR  bool
}

var radarIndices = []radarDef{
	{Code: "000300", Name: "沪深300", Style: "大盘核心"},
	{Code: "H00300", Name: "沪深300全收益", Style: "含分红再投", IsTR: true},
	{Code: "000905", Name: "中证500", Style: "中盘成长"},
	{Code: "000852", Name: "中证1000", Style: "小盘弹性"},
	{Code: "000922", Name: "中证红利", Style: "高股息防御"},
	{Code: "000688", Name: "科创50", Style: "硬科技成长"},
}

type radarRow struct {
	Code      string   `json:"code"`
	Name      string   `json:"name"`
	Style     string   `json:"style"`
	IsTR      bool     `json:"isTR,omitempty"`
	Date      string   `json:"date"`
	Close     float64  `json:"close"`
	PE        *float64 `json:"pe"`
	ERP       *float64 `json:"erp"`       // 1/PE - 10Y国债，百分点形式如 5.8
	PEPct5Y   *float64 `json:"pePct5Y"`   // 近 5 年 PE 分位（0–100）
	PEPct10Y  *float64 `json:"pePct10Y"`  // 近 10 年 PE 分位（0–100）
	Ret20D    *float64 `json:"ret20d"`    // 近 20 交易日涨跌幅（小数）
	Ret60D    *float64 `json:"ret60d"`    // 近 60 交易日涨跌幅
	Ret250D   *float64 `json:"ret250d"`   // 近 250 交易日涨跌幅
	MA200Dist *float64 `json:"ma200Dist"` // (Close/MA200 - 1)
	Tier      string   `json:"tier"`      // cheap / fair / expensive
	Error     string   `json:"error,omitempty"`
}

type radarResp struct {
	AsOf      time.Time  `json:"asOf"`
	BondY10   *float64   `json:"bondY10"`
	BondDate  string     `json:"bondDate,omitempty"`
	TRSpread1 *float64   `json:"trSpread1y,omitempty"` // 沪深300全收益 − 价格指数 近1年收益差（分红贡献）
	TRSpread5 *float64   `json:"trSpread5y,omitempty"` // 近5年年化分红收益差
	Items     []radarRow `json:"items"`
}

var (
	radarMu    sync.Mutex
	radarCache *radarResp
)

func (s *Server) radar(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("refresh") == "1"
	radarMu.Lock()
	if !force && radarCache != nil && time.Since(radarCache.AsOf) < time.Hour {
		c := *radarCache
		radarMu.Unlock()
		writeJSON(w, http.StatusOK, c)
		return
	}
	radarMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 读取本地最新 10 年国债收益率（%）用于计算各宽基 ERP
	var bondY10 *float64
	var bondDate string
	if pt, ok, err := s.store.LatestPoint(ctx, ds.CNBondY10); err == nil && ok {
		v := pt.Value
		bondY10, bondDate = &v, pt.Date
	}

	to := s.now()
	from := to.AddDate(-10, 0, 0)
	client := &http.Client{Timeout: 15 * time.Second}

	items := make([]radarRow, len(radarIndices))
	rowsMap := make([][]csindex.Row, len(radarIndices))
	var wg sync.WaitGroup
	for i, def := range radarIndices {
		wg.Add(1)
		go func(idx int, d radarDef) {
			defer wg.Done()
			rows, err := csindex.FetchPerf(ctx, client, d.Code, from, to)
			if err != nil {
				items[idx] = radarRow{Code: d.Code, Name: d.Name, Style: d.Style, IsTR: d.IsTR, Error: err.Error()}
				return
			}
			rowsMap[idx] = rows
			items[idx] = summarizeRadar(d, rows, bondY10)
		}(i, def)
	}
	wg.Wait()

	resp := radarResp{AsOf: s.now(), BondY10: bondY10, BondDate: bondDate, Items: items}
	// 计算 000300 vs H00300 的分红复利差
	if len(rowsMap[0]) > 250 && len(rowsMap[1]) > 250 {
		if sp := trSpread(rowsMap[0], rowsMap[1], 250, 1); sp != nil {
			resp.TRSpread1 = sp
		}
		if sp := trSpread(rowsMap[0], rowsMap[1], 1250, 5); sp != nil {
			resp.TRSpread5 = sp
		}
	}

	radarMu.Lock()
	radarCache = &resp
	radarMu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

func summarizeRadar(d radarDef, rows []csindex.Row, bondY10 *float64) radarRow {
	out := radarRow{Code: d.Code, Name: d.Name, Style: d.Style, IsTR: d.IsTR}
	var closes []float64
	var pes []float64
	var lastDate string
	var lastPE float64
	for _, r := range rows {
		if r.Close != nil && *r.Close > 0 {
			closes = append(closes, *r.Close)
			lastDate = r.Date
		}
		if r.PE != nil && *r.PE > 0 {
			pes = append(pes, *r.PE)
			lastPE = *r.PE
		}
	}
	if len(closes) == 0 {
		out.Error = "无行情数据"
		return out
	}
	n := len(closes)
	last := closes[n-1]
	out.Date, out.Close = lastDate, last

	retOf := func(back int) *float64 {
		if n <= back {
			return nil
		}
		v := last/closes[n-1-back] - 1
		return &v
	}
	out.Ret20D = retOf(20)
	out.Ret60D = retOf(60)
	out.Ret250D = retOf(250)
	if n >= 200 {
		sum := 0.0
		for _, c := range closes[n-200:] {
			sum += c
		}
		ma := sum / 200
		if ma > 0 {
			v := last/ma - 1
			out.MA200Dist = &v
		}
	}
	if lastPE > 0 {
		pe := lastPE
		out.PE = &pe
		if bondY10 != nil {
			erp := 100.0/pe - *bondY10 // 百分点，例如 7.8% - 1.8% = 6.0%
			out.ERP = &erp
		}
		if len(pes) >= 60 {
			p10 := pctRank(pes, lastPE)
			out.PEPct10Y = &p10
			if p10 <= 25 {
				out.Tier = "cheap"
			} else if p10 >= 75 {
				out.Tier = "expensive"
			} else {
				out.Tier = "fair"
			}
		}
		if len(pes) >= 250 {
			win := pes
			if len(win) > 1250 {
				win = win[len(win)-1250:]
			}
			p5 := pctRank(win, lastPE)
			out.PEPct5Y = &p5
		}
	}
	return out
}

func pctRank(xs []float64, cur float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	le := 0
	for _, x := range xs {
		if x <= cur {
			le++
		}
	}
	return math.Round(float64(le)/float64(len(xs))*1000) / 10
}

func trSpread(px, tr []csindex.Row, days int, years float64) *float64 {
	var pClose, tClose []float64
	for _, r := range px {
		if r.Close != nil && *r.Close > 0 {
			pClose = append(pClose, *r.Close)
		}
	}
	for _, r := range tr {
		if r.Close != nil && *r.Close > 0 {
			tClose = append(tClose, *r.Close)
		}
	}
	if len(pClose) <= days || len(tClose) <= days {
		return nil
	}
	pr := pClose[len(pClose)-1] / pClose[len(pClose)-1-days]
	trr := tClose[len(tClose)-1] / tClose[len(tClose)-1-days]
	if pr <= 0 || trr <= 0 {
		return nil
	}
	sp := math.Pow(trr, 1/years) - math.Pow(pr, 1/years)
	return &sp
}
