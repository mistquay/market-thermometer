package eastmoney

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	ds "market-thermometer/internal/datasource"
)

// 证券名称：push2 行情列表快照，按代码升序分页，每页最多 100 条（沪深 A 股约 60 页、场内基金约 15 页）。
// 实测（2026-11-25）：每 300ms 一页时第 41 页被断开，随后整个 push2 列表接口对本 IP 不可用（说明 push2 也会封 IP）。
// 所以每次最多取 namePagesPerCall 页、页间隔 1 秒，返回游标下次继续。列表含已退市股票。
const (
	clistURL         = "https://push2.eastmoney.com/api/qt/clist/get?pn=%d&pz=100&po=0&np=1&fltt=2&invt=2&fid=f12&fs=%s&fields=f12,f14"
	namePagesPerCall = 30
)

// 只取场内基金（约 15 页）：股票名称由 stockdb 在同步日线时离线提供，不再为它请求约 60 页的 A 股列表。
var nameLists = []string{
	"b:MK0021,b:MK0022,b:MK0023,b:MK0024,b:MK0404,b:MK0405,b:MK0406,b:MK0407", // 场内 ETF、LOF
}

// SecurityNames 从游标处（"列表序号:页码"，空为从头）开始取名称，最多 namePagesPerCall 页。
func (s *Source) SecurityNames(ctx context.Context, cursor string) (map[string]string, string, error) {
	list, page := 0, 1
	if cursor != "" {
		a, b, _ := strings.Cut(cursor, ":")
		list, _ = strconv.Atoi(a)
		page, _ = strconv.Atoi(b)
		if list < 0 || list >= len(nameLists) || page < 1 {
			list, page = 0, 1
		}
	}
	out := map[string]string{}
	for fetched := 0; list < len(nameLists); fetched++ {
		next := fmt.Sprintf("%d:%d", list, page)
		if fetched == namePagesPerCall {
			return out, next, nil
		}
		if fetched > 0 {
			select {
			case <-ctx.Done():
				return out, next, ctx.Err()
			case <-time.After(time.Second):
			}
		}
		body, err := ds.GetBodyOnce(ctx, s.http, fmt.Sprintf(clistURL, page, nameLists[list]), nil)
		if err != nil {
			return out, next, fmt.Errorf("eastmoney 证券列表（%s）：%w", next, err)
		}
		var r struct {
			Data *struct {
				Total int `json:"total"`
				Diff  []struct {
					Code string `json:"f12"`
					Name string `json:"f14"`
				} `json:"diff"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return out, next, fmt.Errorf("eastmoney 证券列表：%w", err)
		}
		if r.Data != nil {
			for _, d := range r.Data.Diff {
				if ds.KindOf(d.Code) != "" && d.Name != "" && d.Name != "-" {
					out[d.Code] = d.Name
				}
			}
		}
		if r.Data == nil || len(r.Data.Diff) == 0 || page*100 >= r.Data.Total {
			list, page = list+1, 1
		} else {
			page++
		}
	}
	return out, "", nil
}
