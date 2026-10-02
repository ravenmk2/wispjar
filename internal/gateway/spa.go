package gateway

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/web"
)

func spaHandler() gin.HandlerFunc {
	dist := web.DistFS()
	fileServer := http.FileServerFS(dist)
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		index = []byte("<!doctype html><title>wispjar</title>\n")
	}
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if p == "/api" || strings.HasPrefix(p, "/api/") ||
			(c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) {
			respond.Error(c, http.StatusNotFound, respond.CodeNotFound, "资源不存在", nil)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+p), "/")
		if fi, err := fs.Stat(dist, name); err == nil && !fi.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	}
}
