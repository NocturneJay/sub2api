package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

type codexOutboundUAContextKey struct{}

// WithCodexAccountOutboundUserAgent 把账号级出站 UA 候选放进 ctx，供拿不到账号句柄的凭据面
// （repository 层对 auth.openai.com 的换 Token / 刷新）取同一台机器的身份：真实客户端的登录刷新
// 与推理请求来自同一台机器，凭据面与推理面不该各报一套 OS/终端。
func WithCodexAccountOutboundUserAgent(ctx context.Context, account *Account) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ua := codexAccountOutboundUserAgent(account)
	if ua == "" {
		return ctx
	}
	return context.WithValue(ctx, codexOutboundUAContextKey{}, ua)
}

// CodexAuthIdentityFromContext 返回凭据面身份对（User-Agent + originator，不含 version）：
// ctx 带账号级候选时按其解析（版本段仍由生效版本重建、originator 按首段配对），
// 否则回退规范身份 CodexCanonicalAuthIdentity。
func CodexAuthIdentityFromContext(ctx context.Context) (userAgent, originator string) {
	if ctx != nil {
		if ua, _ := ctx.Value(codexOutboundUAContextKey{}).(string); strings.TrimSpace(ua) != "" {
			identity := resolveCodexOutboundIdentity(ua)
			return identity.userAgent, identity.originator
		}
	}
	return CodexCanonicalAuthIdentity()
}

// 账号级稳定机器画像（aicat 自研，2026-09-08）。
//
// 背景：出站 User-Agent 原本对所有账号、所有用户都是同一串（同一台 Ubuntu/xterm），
// 上游视角是 N 个互不相干的 ChatGPT 账号全部运行在一模一样的机器上。真实用户一个账号
// 对应一台（或少数几台）固定机器。本文件让每个上游账号按其命名空间确定性地得到一套
// OS/架构/终端指纹，跨重启、跨影子账号稳定；版本段仍由生效版本重建、originator 仍按
// 首段配对，issue #3901 的配对不变式不受影响。
//
// 画像池取自 2026-09 生产入站真实 Codex 客户端 UA 的高频组合（Windows Codex Desktop/TUI
// 为主，其次 macOS 26 + iTerm/Terminal、Ubuntu 24.04 + xterm），权重按出现频次粗略放大。
// 只放真实存在过的组合，不要手工发明 OS/终端字符串。
var codexAccountUAProfiles = []string{
	"(Windows 10.0.26200; x86_64) unknown",
	"(Windows 10.0.26200; x86_64) unknown",
	"(Windows 10.0.26200; x86_64) unknown",
	"(Windows 10.0.26100; x86_64) unknown",
	"(Windows 10.0.26100; x86_64) unknown",
	"(Windows 10.0.19045; x86_64) unknown",
	"(Windows 10.0.22631; x86_64) unknown",
	"(Mac OS 26.5.0; arm64) iTerm.app/3.6.10",
	"(Mac OS 26.6.2; arm64) Apple_Terminal/470.2",
	"(Ubuntu 24.4.0; x86_64) xterm-256color",
}

// codexAccountUAProfileEnabled 由 gateway.disable_codex_account_ua_profile 取反发布，默认开启。
// 与 codexIdentityEnforcement 同理：出站收口是纯函数，拿不到配置，由服务构造时发布进程级快照。
var codexAccountUAProfileEnabled = func() *atomic.Bool {
	v := &atomic.Bool{}
	v.Store(true)
	return v
}()

// SetCodexAccountUAProfileEnabled 发布「账号级稳定机器画像」开关快照。
func SetCodexAccountUAProfileEnabled(enabled bool) {
	codexAccountUAProfileEnabled.Store(enabled)
}

// codexUATrailer 拼出 codex-rs UA 末尾的 `(name; version)` 官方客户端标识组。
func codexUATrailer(name, version string) string {
	return " (" + name + "; " + version + ")"
}

// codexAccountUAProfile 按上游账号命名空间确定性地选一套机器画像；无命名空间返回空串。
func codexAccountUAProfile(namespace string) string {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" || len(codexAccountUAProfiles) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte("codex-account-ua-profile:v1:" + namespace))
	idx := binary.BigEndian.Uint64(sum[:8]) % uint64(len(codexAccountUAProfiles))
	return codexAccountUAProfiles[idx]
}

// codexAccountOutboundUserAgent 返回账号级出站 User-Agent 候选，供 enforceCodexIdentityHeadersWithUA /
// resolveCodexOutboundIdentity 当作管理员显式配置对待（版本段由生效版本重建、originator 按首段配对）：
//  1. credentials.user_agent（管理员显式配置）优先，逐字返回；
//  2. 画像开启且账号有上游命名空间（chatgpt_account_id / 指纹 seed / setup-token 指纹）时，
//     按命名空间稳定选一套画像，拼成 codex-rs 现行形态 `codex-tui/{ver} {profile} (codex-tui; {ver})`；
//  3. 其余返回空串，走规范 UA。
//
// 同一 ChatGPT 账号的多条本地记录共享同一台机器；影子账号不持凭据，调用方须先解析到母账号
// （codexAccountIdentitySource / credentialAccount）再传入。
func codexAccountOutboundUserAgent(account *Account) string {
	if account == nil {
		return ""
	}
	if ua := strings.TrimSpace(account.GetOpenAIUserAgent()); ua != "" {
		return ua
	}
	if !codexAccountUAProfileEnabled.Load() {
		return ""
	}
	profile := codexAccountUAProfile(codexAccountIdentityNamespace(account))
	if profile == "" {
		return ""
	}
	version := codexClientVersionFromUA(codexCanonicalUserAgent())
	return openai.CodexDefaultOriginator + "/" + version + " " + profile + codexUATrailer(openai.CodexDefaultOriginator, version)
}
