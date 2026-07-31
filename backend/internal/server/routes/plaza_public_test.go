//go:build unit

package routes

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsPubliclyRoutableIP(t *testing.T) {
	// 归因可信才计数：反代未配置 trusted_proxies 时 c.ClientIP() 返回的是
	// 环回/内网地址，此时必须判为不可信，否则所有访客会共用同一个限流桶。
	notCounted := []string{
		"127.0.0.1",    // 反代与本机回源
		"::1",          //
		"10.1.2.3",     // 容器网络 / 内网
		"172.16.5.9",   //
		"192.168.1.10", //
		"169.254.10.1", // 链路本地
		"fe80::1",      //
		"0.0.0.0",      // 未指定
		"",             // 解析失败
		"not-an-ip",    //
		"1.2.3.4:5678", // 带端口，ParseIP 失败
	}
	for _, ip := range notCounted {
		require.Falsef(t, isPubliclyRoutableIP(ip), "%q 应判为不可信归因（不计数）", ip)
	}

	counted := []string{
		"8.8.8.8",
		"152.53.90.10",
		"2001:4860:4860::8888",
	}
	for _, ip := range counted {
		require.Truef(t, isPubliclyRoutableIP(ip), "%q 应判为公网可路由（计数）", ip)
	}
}
