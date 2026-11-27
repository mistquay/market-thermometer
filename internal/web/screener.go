package web

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"market-thermometer/internal/store"
)

type screenerCache struct {
	mu      sync.Mutex
	asOf    string
	builtAt time.Time
	rows    []store.ScreenerRow
}

// screener 基于本地 stock_bar + stock_daily_ext 进行全市场筛选与排序（首次按最新日构建缓存，后续毫秒级响应）。
func (s *Server) screener(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	refresh := r.URL.Query().Get("refresh") == "1"
	rows, asOf, elapsedMs, err := s.loadScreener(ctx, refresh)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind == "" {
		kind = "stock"
	}
	keyword := strings.ToLower(strings.TrimSpace(q.Get("q")))
	peMin, hasPEMin := parseOptFloat(q.Get("peMin"))
	peMax, hasPEMax := parseOptFloat(q.Get("peMax"))
	pbMin, hasPBMin := parseOptFloat(q.Get("pbMin"))
	pbMax, hasPBMax := parseOptFloat(q.Get("pbMax"))
	mvMin, hasMVMin := parseOptFloat(q.Get("mvMin"))
	mvMax, hasMVMax := parseOptFloat(q.Get("mvMax"))
	amtMin, hasAmtMin := parseOptFloat(q.Get("amtMin"))
	ret20Min, hasRet20Min := parseOptFloat(q.Get("ret20Min"))
	ret20Max, hasRet20Max := parseOptFloat(q.Get("ret20Max"))
	aboveMA := q.Get("aboveMa200") // "" | "1" | "0"

	matched := make([]store.ScreenerRow, 0, 256)
	for _, row := range rows {
		if kind != "all" && row.Kind != kind {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(row.Code), keyword) && !strings.Contains(strings.ToLower(row.Name), keyword) {
			continue
		}
		if hasPEMin && (row.PETTM == nil || *row.PETTM < peMin) {
			continue
		}
		if hasPEMax && (row.PETTM == nil || *row.PETTM > peMax) {
			continue
		}
		if hasPBMin && (row.PB == nil || *row.PB < pbMin) {
			continue
		}
		if hasPBMax && (row.PB == nil || *row.PB > pbMax) {
			continue
		}
		if hasMVMin && (row.TotalMV == nil || *row.TotalMV < mvMin) {
			continue
		}
		if hasMVMax && (row.TotalMV == nil || *row.TotalMV > mvMax) {
			continue
		}
		if hasAmtMin && (row.Amount == nil || *row.Amount < amtMin) {
			continue
		}
		if hasRet20Min && (row.Ret20 == nil || *row.Ret20*100 < ret20Min) {
			continue
		}
		if hasRet20Max && (row.Ret20 == nil || *row.Ret20*100 > ret20Max) {
			continue
		}
		if aboveMA == "1" && (row.AboveMA200 == nil || !*row.AboveMA200) {
			continue
		}
		if aboveMA == "0" && (row.AboveMA200 == nil || *row.AboveMA200) {
			continue
		}
		matched = append(matched, row)
	}

	sortKey := q.Get("sort")
	if sortKey == "" {
		sortKey = "mv"
	}
	desc := q.Get("desc") != "0"
	valOf := func(r store.ScreenerRow) (float64, bool) {
		switch sortKey {
		case "pe":
			if r.PETTM != nil {
				return *r.PETTM, true
			}
		case "pb":
			if r.PB != nil {
				return *r.PB, true
			}
		case "ret20":
			if r.Ret20 != nil {
				return *r.Ret20, true
			}
		case "ret60":
			if r.Ret60 != nil {
				return *r.Ret60, true
			}
		case "distMa200":
			if r.DistMA200 != nil {
				return *r.DistMA200, true
			}
		case "amount":
			if r.Amount != nil {
				return *r.Amount, true
			}
		case "close":
			return r.Close, true
		default: // mv
			if r.TotalMV != nil {
				return *r.TotalMV, true
			}
		}
		return 0, false
	}
	sort.SliceStable(matched, func(i, j int) bool {
		vi, oki := valOf(matched[i])
		vj, okj := valOf(matched[j])
		if oki != okj {
			return oki // 有值的排前面
		}
		if !oki {
			return matched[i].Code < matched[j].Code
		}
		if vi == vj {
			return matched[i].Code < matched[j].Code
		}
		if desc {
			return vi > vj
		}
		return vi < vj
	})

	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	total := len(matched)
	if len(matched) > limit {
		matched = matched[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"asOf":      asOf,
		"universe":  len(rows),
		"total":     total,
		"elapsedMs": elapsedMs,
		"items":     matched,
	})
}

func (s *Server) loadScreener(ctx context.Context, refresh bool) ([]store.ScreenerRow, string, int64, error) {
	began := time.Now()
	s.screenerCache.mu.Lock()
	defer s.screenerCache.mu.Unlock()
	if !refresh && len(s.screenerCache.rows) > 0 && time.Since(s.screenerCache.builtAt) < 10*time.Minute {
		return s.screenerCache.rows, s.screenerCache.asOf, time.Since(began).Milliseconds(), nil
	}
	rows, asOf, err := s.store.BuildScreenerSnapshot(ctx)
	if err != nil {
		return nil, "", 0, err
	}
	s.screenerCache.rows, s.screenerCache.asOf, s.screenerCache.builtAt = rows, asOf, time.Now()
	return rows, asOf, time.Since(began).Milliseconds(), nil
}

func parseOptFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}
