// Package importer 把外部整理好的数据（如用户脚本拉取的场内基金日线）导入本地库。
package importer

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/store"
)

// Result 是一次导入的摘要。
type Result struct {
	Codes int
	Rows  int
}

// ParseBars 解析 CSV（UTF-8，带表头）：code,date,close[,mult]。close 为不复权收盘价，mult 为除权乘数（缺省 1）。
// 任何一行不合法都返回错误（带行号），不做部分导入。
func ParseBars(r io.Reader) (map[string][]ds.StockBar, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	head, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("读取表头：%w", err)
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	for _, need := range []string{"code", "date", "close"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("表头缺少 %s 列（需要 code,date,close[,mult]）", need)
		}
	}
	out := map[string][]ds.StockBar{}
	var errs []error
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("第 %d 行：%w", line, err)
		}
		get := func(k string) string {
			if i, ok := col[k]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		code, date := get("code"), get("date")
		if len(date) == 8 && !strings.Contains(date, "-") {
			date = date[:4] + "-" + date[4:6] + "-" + date[6:]
		}
		closeV, e1 := strconv.ParseFloat(get("close"), 64)
		mult, e2 := 1.0, error(nil)
		if s := get("mult"); s != "" {
			mult, e2 = strconv.ParseFloat(s, 64)
		}
		_, e3 := time.Parse("2006-01-02", date)
		switch {
		case ds.KindOf(code) == "":
			errs = append(errs, fmt.Errorf("第 %d 行：代码 %q 不是沪深 A 股或场内基金", line, code))
		case e3 != nil:
			errs = append(errs, fmt.Errorf("第 %d 行：日期 %q 应为 YYYY-MM-DD 或 YYYYMMDD", line, get("date")))
		case e1 != nil || closeV <= 0:
			errs = append(errs, fmt.Errorf("第 %d 行：收盘价 %q 不合法", line, get("close")))
		case e2 != nil || mult <= 0:
			errs = append(errs, fmt.Errorf("第 %d 行：除权乘数 %q 不合法", line, get("mult")))
		default:
			out[code] = append(out[code], ds.StockBar{Date: date, Close: closeV, Mult: mult})
		}
		if len(errs) >= 20 {
			errs = append(errs, errors.New("错误过多，已停止检查"))
			break
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	for code, bars := range out {
		sort.Slice(bars, func(i, j int) bool { return bars[i].Date < bars[j].Date })
		for i := 1; i < len(bars); i++ {
			if bars[i].Date == bars[i-1].Date {
				return nil, fmt.Errorf("%s 的 %s 重复", code, bars[i].Date)
			}
		}
	}
	return out, nil
}

// ImportBars 写入解析好的日线（同一代码同一天已有数据时覆盖），来源记为 source。
func ImportBars(ctx context.Context, st *store.Store, bars map[string][]ds.StockBar, source string, now time.Time) (Result, error) {
	codes := make([]string, 0, len(bars))
	rows := 0
	for c, b := range bars {
		codes = append(codes, c)
		rows += len(b)
	}
	sort.Strings(codes)
	if err := st.EnsureStocks(ctx, codes, now); err != nil {
		return Result{}, err
	}
	if err := st.SaveStockBars(ctx, source, bars); err != nil {
		return Result{}, err
	}
	return Result{Codes: len(codes), Rows: rows}, nil
}
