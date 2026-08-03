//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestProbeRegistry() *channelMonitorProbeRegistry {
	return &channelMonitorProbeRegistry{entries: make(map[string]time.Time)}
}

func TestChannelMonitorProbeNonceConsumedExactlyOnce(t *testing.T) {
	r := newTestProbeRegistry()
	now := time.Unix(1700000000, 0)

	nonce := r.issue(now)
	require.NotEmpty(t, nonce)

	require.True(t, r.consume(nonce, now.Add(time.Second)), "首次消费应成功")
	// 一次性是本设计的核心：随机数即便泄露给第三方也无价值，因为它已被消费。
	require.False(t, r.consume(nonce, now.Add(2*time.Second)), "重放必须失败")
}

func TestChannelMonitorProbeNonceExpires(t *testing.T) {
	r := newTestProbeRegistry()
	now := time.Unix(1700000000, 0)

	nonce := r.issue(now)
	require.NotEmpty(t, nonce)

	require.False(t, r.consume(nonce, now.Add(channelMonitorProbeTTL+time.Second)), "过期后不得通过")
	// 过期项在消费尝试时就被删除，不依赖额外的清理时机。
	require.Empty(t, r.entries)
}

func TestChannelMonitorProbeRejectsUnknownAndEmptyNonce(t *testing.T) {
	r := newTestProbeRegistry()
	now := time.Unix(1700000000, 0)

	require.False(t, r.consume("", now), "空随机数必须拒绝")
	require.False(t, r.consume("not-a-real-nonce", now), "未签发的随机数必须拒绝")
}

func TestChannelMonitorProbeNoncesAreDistinct(t *testing.T) {
	r := newTestProbeRegistry()
	now := time.Unix(1700000000, 0)

	seen := make(map[string]struct{}, 256)
	for i := 0; i < 256; i++ {
		nonce := r.issue(now)
		require.NotEmpty(t, nonce)
		_, dup := seen[nonce]
		require.False(t, dup, "随机数不得重复")
		seen[nonce] = struct{}{}
	}
}

func TestChannelMonitorProbeRegistryBoundedAndSelfCleaning(t *testing.T) {
	r := newTestProbeRegistry()
	now := time.Unix(1700000000, 0)

	// 签满容量，且全部不消费——模拟 endpoint 配错、探测请求从未抵达网关。
	for i := 0; i < channelMonitorProbeMaxEntries; i++ {
		require.NotEmpty(t, r.issue(now))
	}
	require.Len(t, r.entries, channelMonitorProbeMaxEntries)

	// 容量已满且都未过期时拒绝签发：宁可这次探测不打标记，也不让注册表无限增长。
	require.Empty(t, r.issue(now), "容量满且无过期项时应拒绝签发")

	// 时间推进到全部过期后，签发重新可用，且注册表被清理。
	later := now.Add(channelMonitorProbeTTL + time.Second)
	require.NotEmpty(t, r.issue(later), "过期项清理后应恢复签发")
	require.Len(t, r.entries, 1)
}
