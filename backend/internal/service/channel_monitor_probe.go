package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// 渠道监控探测标记。
//
// 背景：渠道监控是通过本站自己的网关发真实请求来做健康检查的，因此每次探测都会在
// usage_logs 里留下一条记录，混在用户真实用量里。管理端「使用记录」需要能把这类
// 记录排除掉。
//
// 为什么不能靠 User-Agent 或 API 密钥直接认：
//   - 监控走 Go 默认 UA（Go-http-client/2.0），而真实用户里也有人用 Go 客户端；
//     anthropic 那条监控走的是 identity_service 里伪装成 claude-cli 的 UA，
//     和真实 claude-cli 流量完全无法区分。
//   - 监控用的是管理员自己的 API 密钥，同一把密钥里还混着管理员的真实使用。
//
// 所以改为在发起端打标记：监控请求带一个一次性随机数，网关侧中间件校验并消费它，
// 校验通过才把「本请求是渠道监控探测」写进 request context，最终落到 usage_logs
// 的独立列上。外部请求无法凭空伪造该标记。
//
// 为什么不复用 User-Agent 作为标记：管理员可以给监控配自定义 UA（生产上
// anthropic 那条监控就配了 claude-cli 的 UA），UA 既可能被上游校验，也参与
// 本站自己的客户端识别（checkClaudeCodeRestriction）。占用或改写 UA 会改变
// 监控的实际行为，所以标记必须走独立字段。
//
// 用一次性随机数而不是长期令牌，是因为监控的 endpoint 是管理员可配置的：
// 万一有人把它指向外部服务商，长期令牌就泄露给第三方了。一次性随机数即便泄露
// 也没有价值——它要么已经被本站网关消费掉，要么两分钟后过期。
//
// 注册表放在进程内存里而不是 Redis：监控 runner 与网关在同一个进程内，单实例
// 部署下这就够了。多实例下最坏情况是随机数在 A 实例签发、请求被负载均衡打到
// B 实例，校验不通过 → 该条记录不带标记、照常显示在列表里。是优雅降级，
// 不会出错，也不会误伤别人的记录。

const (
	// ChannelMonitorProbeHeader 承载一次性随机数。
	ChannelMonitorProbeHeader = "X-Sub2API-Monitor-Probe"

	// channelMonitorProbeTTL 是随机数的有效期。探测请求从签发到抵达网关只有一次
	// 本机 HTTP 往返，两分钟足够宽裕，同时把泄露后的可用窗口压到很短。
	channelMonitorProbeTTL = 2 * time.Minute

	// channelMonitorProbeMaxEntries 是注册表的容量上限，防止签发后从未被消费
	// （例如 endpoint 配错、请求超时）的随机数无限堆积。超出时清理过期项，
	// 仍超出则拒绝签发（本次探测不带标记，降级为普通记录）。
	channelMonitorProbeMaxEntries = 4096
)

// channelMonitorProbeRegistry 管理一次性随机数的签发与消费。
type channelMonitorProbeRegistry struct {
	mu      sync.Mutex
	entries map[string]time.Time // nonce -> 过期时刻
}

var channelMonitorProbes = &channelMonitorProbeRegistry{
	entries: make(map[string]time.Time),
}

// issue 签发一个随机数。返回空串表示本次不打标记（调用方应照常发请求，
// 只是这条记录会以普通记录的形态落库）。
func (r *channelMonitorProbeRegistry) issue(now time.Time) string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	nonce := base64.RawURLEncoding.EncodeToString(buf)

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) >= channelMonitorProbeMaxEntries {
		r.purgeExpiredLocked(now)
		if len(r.entries) >= channelMonitorProbeMaxEntries {
			return ""
		}
	}
	r.entries[nonce] = now.Add(channelMonitorProbeTTL)
	return nonce
}

// consume 校验并一次性消费随机数。未命中或已过期返回 false。
func (r *channelMonitorProbeRegistry) consume(nonce string, now time.Time) bool {
	if nonce == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	expiresAt, ok := r.entries[nonce]
	if !ok {
		return false
	}
	// 无论是否过期都删除：命中即消费，过期项顺手清掉。
	delete(r.entries, nonce)
	return now.Before(expiresAt)
}

// purgeExpiredLocked 清理已过期项。调用方必须持有锁。
func (r *channelMonitorProbeRegistry) purgeExpiredLocked(now time.Time) {
	for nonce, expiresAt := range r.entries {
		if !now.Before(expiresAt) {
			delete(r.entries, nonce)
		}
	}
}

// IssueChannelMonitorProbeNonce 供渠道监控在发起探测前调用。
// 返回空串表示本次不打标记。
func IssueChannelMonitorProbeNonce() string {
	return channelMonitorProbes.issue(time.Now())
}

// ConsumeChannelMonitorProbeNonce 供网关中间件调用，校验并消费随机数。
func ConsumeChannelMonitorProbeNonce(nonce string) bool {
	return channelMonitorProbes.consume(nonce, time.Now())
}

// channelMonitorProbeCtxKey 是「本请求已确认为渠道监控探测」的 context 键。
// 用私有零尺寸类型作键，避免与其它包的 context 值碰撞。
type channelMonitorProbeCtxKey struct{}

// WithChannelMonitorProbe 标记该 context 对应的请求是渠道监控探测。
// 只应由校验通过随机数的网关中间件调用。
func WithChannelMonitorProbe(ctx context.Context) context.Context {
	return context.WithValue(ctx, channelMonitorProbeCtxKey{}, true)
}

// IsChannelMonitorProbe 判断当前请求是否为渠道监控探测。
// 在构造 UsageLog 时读取，落到 usage_logs.is_channel_monitor 上。
func IsChannelMonitorProbe(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	flag, _ := ctx.Value(channelMonitorProbeCtxKey{}).(bool)
	return flag
}
