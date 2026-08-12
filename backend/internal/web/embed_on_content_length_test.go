//go:build embed

package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestWriteIndexHTMLSetsContentLength 锁定 AICat 自研改动（2026-08-12）。
//
// index.html 必须带 Content-Length 而不是走 Transfer-Encoding: chunked。原因见
// writeIndexHTML 的函数注释：Caddy 把「Content-Length 未知」视为流式响应并在每次
// 写入后立即 flush，逐块 flush 会打断压缩器的滑动窗口，使边缘压缩率从 2.77x 崩到
// 1.29x（实测 381309 字节的首页：正常应压到 ~137KB，chunked 下只有 ~296KB）。
//
// ⚠️ 同步上游后若本用例失败，说明 writeIndexHTML 又被替换回裸的 c.Data 了。
// 请把 embed_on.go 里所有 index.html 出口改回 writeIndexHTML，不要删本用例。
func TestWriteIndexHTMLSetsContentLength(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 远超 net/http 内部嗅探缓冲（2KB），确保不会因为体积小而自动带上 Content-Length
	body := []byte(strings.Repeat("<p>aicat</p>", 8192))

	r := gin.New()
	r.GET("/", func(c *gin.Context) { writeIndexHTML(c, body) })

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if len(resp.TransferEncoding) > 0 {
		t.Errorf("响应走了 %v，期望带 Content-Length 的非分块传输；"+
			"这会让 Caddy 逐块 flush 并摧毁边缘压缩率", resp.TransferEncoding)
	}
	if resp.ContentLength != int64(len(body)) {
		t.Errorf("Content-Length = %d，期望 %d", resp.ContentLength, len(body))
	}
	if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q，期望 text/html; charset=utf-8", got)
	}
}

// TestServeIndexHTMLFromFSSetsContentLength 覆盖包级 serveIndexHTML（从 fs.FS 读取
// 的那条路径），确保它也走 writeIndexHTML。
func TestServeIndexHTMLFromFSSetsContentLength(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dist, err := fs.Sub(frontendFS, "dist")
	if err != nil {
		t.Skipf("取不到内嵌 dist，跳过: %v", err)
	}

	r := gin.New()
	r.GET("/", func(c *gin.Context) { serveIndexHTML(c, dist) })

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if len(resp.TransferEncoding) > 0 {
		t.Errorf("响应走了 %v，期望带 Content-Length 的非分块传输", resp.TransferEncoding)
	}
	if resp.ContentLength <= 0 {
		t.Errorf("Content-Length = %d，期望 > 0", resp.ContentLength)
	}
}
