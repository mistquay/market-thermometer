package web

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	sessionCookie = "mt_session"
	sessionTTL    = 7 * 24 * time.Hour

	loginMaxFails = 5
	loginWindow   = 15 * time.Minute
)

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// loginLimiter 按来源 IP 限制登录失败次数：窗口内失败达到上限后，直到窗口结束前拒绝登录。
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{fails: map[string][]time.Time{}} }

func (l *loginLimiter) recent(ip string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range l.fails[ip] {
		if now.Sub(t) < loginWindow {
			keep = append(keep, t)
		}
	}
	if keep == nil {
		delete(l.fails, ip)
	} else {
		l.fails[ip] = keep
	}
	return keep
}

// Blocked 返回该 IP 是否已被锁定，以及剩余锁定时间。
func (l *loginLimiter) Blocked(ip string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.recent(ip, now)
	if len(r) < loginMaxFails {
		return false, 0
	}
	return true, loginWindow - now.Sub(r[0])
}

func (l *loginLimiter) Fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.recent(ip, now), now)
}

func (l *loginLimiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if ip := r.Header.Get("X-Real-IP"); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sameOrigin：带 Origin 头的写请求必须与 Host 一致，防 CSRF（配合 SameSite=Lax）。
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true // 非浏览器客户端（curl 等）不带 Origin，仍受会话 Cookie 约束
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
