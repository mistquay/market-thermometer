// Package ws 提供零外部依赖的最小 RFC 6455 WebSocket 客户端（支持 ws/wss 与 HTTP 代理 CONNECT 隧道）。
// 用于连接币安现货 bookTicker 与 Polymarket CLOB 实时盘口推送。
package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Conn 是一个客户端 WebSocket 连接。读操作由单个 goroutine 串行调用；写操作并发安全。
type Conn struct {
	netConn net.Conn
	br      *bufio.Reader
	wmu     sync.Mutex
	closed  bool
}

// Dial 建立 WebSocket 连接；proxyURL 非空时经 HTTP CONNECT 隧道连接（支持 wss）。
func Dial(ctx context.Context, rawURL, proxyURL string) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	isTLS := u.Scheme == "wss" || u.Scheme == "https"
	if !isTLS && u.Scheme != "ws" && u.Scheme != "http" {
		return nil, fmt.Errorf("ws: unsupported scheme %q", u.Scheme)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if isTLS {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	var nc net.Conn
	if proxyURL != "" {
		pu, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("ws proxy: %w", err)
		}
		pHost := pu.Host
		if !strings.Contains(pHost, ":") {
			pHost += ":80"
		}
		nc, err = dialer.DialContext(ctx, "tcp", pHost)
		if err != nil {
			return nil, err
		}
		req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", host, host)
		if pu.User != nil {
			auth := base64.StdEncoding.EncodeToString([]byte(pu.User.String()))
			req += "Proxy-Authorization: Basic " + auth + "\r\n"
		}
		req += "\r\n"
		if dl, ok := ctx.Deadline(); ok {
			_ = nc.SetDeadline(dl)
		}
		if _, err := io.WriteString(nc, req); err != nil {
			nc.Close()
			return nil, err
		}
		br := bufio.NewReader(nc)
		resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("ws proxy connect: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			nc.Close()
			return nil, fmt.Errorf("ws proxy connect status: %s", resp.Status)
		}
		_ = nc.SetDeadline(time.Time{})
	} else {
		nc, err = dialer.DialContext(ctx, "tcp", host)
		if err != nil {
			return nil, err
		}
	}
	if isTLS {
		serverName, _, _ := net.SplitHostPort(host)
		tc := tls.Client(nc, &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			nc.Close()
			return nil, err
		}
		nc = tc
	}
	var keyBytes [16]byte
	_, _ = rand.Read(keyBytes[:])
	key := base64.StdEncoding.EncodeToString(keyBytes[:])
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, u.Host, key)
	if dl, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(dl)
	}
	if _, err := io.WriteString(nc, req); err != nil {
		nc.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(nc, 64*1024)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("ws handshake: %w", err)
	}
	_ = nc.SetDeadline(time.Time{})
	if resp.StatusCode != http.StatusSwitchingProtocols {
		nc.Close()
		return nil, fmt.Errorf("ws handshake status: %s", resp.Status)
	}
	h := sha1.Sum([]byte(key + wsGUID))
	wantAccept := base64.StdEncoding.EncodeToString(h[:])
	if resp.Header.Get("Sec-WebSocket-Accept") != wantAccept {
		nc.Close()
		return nil, errors.New("ws handshake: invalid Sec-WebSocket-Accept")
	}
	return &Conn{netConn: nc, br: br}, nil
}

// WriteText 发送一条文本消息（按 RFC 6455 客户端要求进行掩码）。
func (c *Conn) WriteText(payload []byte) error { return c.writeFrame(0x1, payload) }

// Ping 发送心跳帧。
func (c *Conn) Ping() error { return c.writeFrame(0x9, nil) }

func (c *Conn) writeFrame(opcode byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	var hdr [14]byte
	hdr[0] = 0x80 | (opcode & 0x0f)
	n := len(payload)
	hl := 2
	switch {
	case n < 126:
		hdr[1] = 0x80 | byte(n)
	case n <= 65535:
		hdr[1] = 0x80 | 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(n))
		hl = 4
	default:
		hdr[1] = 0x80 | 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(n))
		hl = 10
	}
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	copy(hdr[hl:hl+4], mask[:])
	hl += 4
	buf := make([]byte, hl+n)
	copy(buf, hdr[:hl])
	for i := range n {
		buf[hl+i] = payload[i] ^ mask[i&3]
	}
	_ = c.netConn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.netConn.Write(buf)
	return err
}

// ReadMessage 读取下一条完整文本/二进制消息（自动响应 Ping、跳过 Pong、收到 Close 返回 io.EOF）。
func (c *Conn) ReadMessage(timeout time.Duration) ([]byte, error) {
	var msg []byte
	for {
		if timeout > 0 {
			_ = c.netConn.SetReadDeadline(time.Now().Add(timeout))
		}
		var h [2]byte
		if _, err := io.ReadFull(c.br, h[:]); err != nil {
			return nil, err
		}
		fin := (h[0] & 0x80) != 0
		op := h[0] & 0x0f
		masked := (h[1] & 0x80) != 0
		plen := uint64(h[1] & 0x7f)
		switch plen {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.br, ext[:]); err != nil {
				return nil, err
			}
			plen = uint64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.br, ext[:]); err != nil {
				return nil, err
			}
			plen = binary.BigEndian.Uint64(ext[:])
		}
		if plen > 16<<20 {
			return nil, errors.New("ws: frame too large")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(c.br, mask[:]); err != nil {
				return nil, err
			}
		}
		data := make([]byte, plen)
		if plen > 0 {
			if _, err := io.ReadFull(c.br, data); err != nil {
				return nil, err
			}
			if masked {
				for i := range data {
					data[i] ^= mask[i&3]
				}
			}
		}
		switch op {
		case 0x8: // close
			_ = c.writeFrame(0x8, nil)
			return nil, io.EOF
		case 0x9: // ping -> pong
			_ = c.writeFrame(0xA, data)
			continue
		case 0xA: // pong
			continue
		case 0x0, 0x1, 0x2:
			msg = append(msg, data...)
			if fin {
				return msg, nil
			}
		}
	}
}

func (c *Conn) Close() error {
	c.wmu.Lock()
	c.closed = true
	c.wmu.Unlock()
	return c.netConn.Close()
}
