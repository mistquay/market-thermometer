// Package webui 嵌入前端构建产物（由 web/ 下 pnpm build 输出到 dist/）。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS 返回以 dist 为根的文件系统。未构建前端时只有 .gitkeep。
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
