package datasource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// UserAgent 是数据请求统一使用的 UA。
const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

// GetBody 发起 GET 请求并返回响应体；网络错误和 5xx 会重试（共 3 次，退避 2s/5s）。
func GetBody(ctx context.Context, c *http.Client, url string, header map[string]string) ([]byte, error) {
	return getWithBackoff(ctx, c, url, header, []time.Duration{0, 2 * time.Second, 5 * time.Second})
}

// GetBodyOnce 只请求一次、不重试。用于会封 IP 的接口：对被封的主机重试只会延长封禁。
func GetBodyOnce(ctx context.Context, c *http.Client, url string, header map[string]string) ([]byte, error) {
	return getWithBackoff(ctx, c, url, header, []time.Duration{0})
}

func getWithBackoff(ctx context.Context, c *http.Client, url string, header map[string]string, backoff []time.Duration) ([]byte, error) {
	var lastErr error
	for _, d := range backoff {
		if d > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(d):
			}
		}
		body, retry, err := getOnce(ctx, c, url, header)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return nil, lastErr
}

func getOnce(ctx context.Context, c *http.Client, url string, header map[string]string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, true, err
	}
	if resp.StatusCode != http.StatusOK {
		retry := (resp.StatusCode >= 500 && resp.StatusCode != http.StatusNotImplemented) || resp.StatusCode == 429
		return nil, retry, &HTTPError{Status: resp.StatusCode}
	}
	return body, false, nil
}

// HTTPError 是非 200 响应。501（腾讯 WAF 拦截页）不重试。
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("http %d", e.Status) }

// IsBlocked 判断错误是否像被服务端封禁 / 限流（403、429、501），调用方应停止请求一段时间。
func IsBlocked(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == 403 || he.Status == 429 || he.Status == 501
	}
	return false
}

// Shanghai 是 A股本地时区。
var Shanghai = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

// LatestFinalDate 返回 A股当前"已定稿"的最后一个日期：15:30 之前只到昨天。
// 插件用它过滤盘中未完成的数据。
func LatestFinalDate(now time.Time) string {
	t := now.In(Shanghai)
	if t.Hour() < 15 || (t.Hour() == 15 && t.Minute() < 30) {
		t = t.AddDate(0, 0, -1)
	}
	return t.Format("2006-01-02")
}

// ClipFinal 去掉晚于 LatestFinalDate 的点。
func ClipFinal(pts []Point, now time.Time) []Point {
	last := LatestFinalDate(now)
	out := pts[:0]
	for _, p := range pts {
		if p.Date <= last {
			out = append(out, p)
		}
	}
	return out
}
