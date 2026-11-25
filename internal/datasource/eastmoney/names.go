package eastmoney

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ds "market-thermometer/internal/datasource"
)

// 证券名称：push2 行情列表快照（push2 不像 push2his 那样封 IP）。每页最多 100 条，
// 沪深 A 股约 60 页、场内基金约 15 页；页间隔 300ms。列表含已退市股票的最后名称。
const (
	clistURL  = "https://push2.eastmoney.com/api/qt/clist/get?pn=%d&pz=100&po=0&np=1&fltt=2&invt=2&fid=f12&fs=%s&fields=f12,f14"
	fsAShares = "m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23,m:0+t:81+s:2048"
	fsFunds   = "b:MK0021,b:MK0022,b:MK0023,b:MK0024,b:MK0404,b:MK0405,b:MK0406,b:MK0407"
	maxPages  = 200
)

// SecurityNames 返回沪深 A 股与场内基金的代码 → 简称。
func (s *Source) SecurityNames(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	for _, fs := range []string{fsAShares, fsFunds} {
		for page := 1; page <= maxPages; page++ {
			if page > 1 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(300 * time.Millisecond):
				}
			}
			body, err := ds.GetBody(ctx, s.http, fmt.Sprintf(clistURL, page, fs), nil)
			if err != nil {
				return nil, fmt.Errorf("eastmoney 证券列表第 %d 页：%w", page, err)
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
				return nil, fmt.Errorf("eastmoney 证券列表：%w", err)
			}
			if r.Data == nil || len(r.Data.Diff) == 0 {
				break
			}
			for _, d := range r.Data.Diff {
				if ds.KindOf(d.Code) != "" && d.Name != "" && d.Name != "-" {
					out[d.Code] = d.Name
				}
			}
			if page*100 >= r.Data.Total {
				break
			}
		}
	}
	if len(out) < 1000 {
		return nil, fmt.Errorf("eastmoney 证券列表只取到 %d 条，疑似接口变化", len(out))
	}
	return out, nil
}
