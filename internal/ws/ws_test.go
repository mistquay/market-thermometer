package ws

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDialAndEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		defer nc.Close()
		br := bufio.NewReader(nc)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		h := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + wsGUID))
		acc := base64.StdEncoding.EncodeToString(h[:])
		_, _ = io.WriteString(nc, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+acc+"\r\n\r\n")
		// 读一帧客户端消息（带掩码）
		var hdr [2]byte
		_, _ = io.ReadFull(br, hdr[:])
		n := int(hdr[1] & 0x7f)
		var mask [4]byte
		_, _ = io.ReadFull(br, mask[:])
		buf := make([]byte, n)
		_, _ = io.ReadFull(br, buf)
		for i := range buf {
			buf[i] ^= mask[i&3]
		}
		// 先发一条 Ping，再把消息不掩码回传
		_, _ = nc.Write([]byte{0x89, 0x00})
		reply := append([]byte("echo:"), buf...)
		_, _ = nc.Write(append([]byte{0x81, byte(len(reply))}, reply...))
		time.Sleep(50 * time.Millisecond)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := Dial(ctx, "ws://"+ln.Addr().String()+"/test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.WriteText([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	msg, err := c.ReadMessage(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(string(msg), "echo:hello") {
		t.Fatalf("got %q want echo:hello", string(msg))
	}
}
