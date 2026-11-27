package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// rotatingFile 是按大小轮转的日志文件（保留最多 backups 份历史），用于 mt serve -log。
type rotatingFile struct {
	path    string
	maxSize int64
	backups int

	mu   sync.Mutex
	f    *os.File
	size int64
}

func openRotatingFile(path string, maxSize int64, backups int) (*rotatingFile, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	r := &rotatingFile{path: path, maxSize: maxSize, backups: backups}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, fi.Size()
	return nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxSize > 0 && r.size+int64(len(p)) > r.maxSize {
		_ = r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) rotate() error {
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
	for i := r.backups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", r.path, i)
		dst := fmt.Sprintf("%s.%d", r.path, i+1)
		_ = os.Remove(dst)
		_ = os.Rename(src, dst)
	}
	if r.backups > 0 {
		dst := fmt.Sprintf("%s.1", r.path)
		_ = os.Remove(dst)
		_ = os.Rename(r.path, dst)
	}
	return r.open()
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
