package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func newCodexUAProfileTestAccount(id int64, chatgptAccountID string) *Account {
	return &Account{
		ID:          id,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": chatgptAccountID},
	}
}

// codex-rs 现行 UA 形态：{originator}/{ver} ({OS} {ver}; {arch}) {terminal} ({originator}; {ver})
var codexModernUAShape = regexp.MustCompile(`^codex-tui/(\S+) \([^;()]+; [^;()]+\) \S+ \(codex-tui; (\S+)\)$`)

func TestCodexAccountOutboundUserAgent_StableModernShape(t *testing.T) {
	SetCodexCanonicalUserAgentResolver(nil)
	SetCodexAccountUAProfileEnabled(true)
	t.Cleanup(func() { SetCodexAccountUAProfileEnabled(true) })

	acct := newCodexUAProfileTestAccount(1, "acct-alpha")
	ua := codexAccountOutboundUserAgent(acct)
	require.NotEmpty(t, ua)
	require.Equal(t, ua, codexAccountOutboundUserAgent(acct), "同一账号必须稳定")

	m := codexModernUAShape.FindStringSubmatch(ua)
	require.NotNil(t, m, ua)
	require.Equal(t, m[1], m[2], "UA 首段与末尾组版本必须一致")
	require.Equal(t, CodexCanonicalClientVersion(), m[1], "版本段来自生效版本")

	// 同一 ChatGPT 账号的另一条本地记录（影子账号解析到母账号后）共享同一台机器
	require.Equal(t, ua, codexAccountOutboundUserAgent(newCodexUAProfileTestAccount(99, "acct-alpha")))

	// 终态收口：originator 按首段配对为 codex-tui，版本已一致故 UA 原样保留
	identity := resolveCodexOutboundIdentity(ua)
	require.Equal(t, "codex-tui", identity.originator)
	require.Equal(t, ua, identity.userAgent)
	require.Equal(t, m[1], identity.version)

	// 画像池覆盖：不同上游账号应落到不止一种机器
	seen := map[string]struct{}{}
	for i := 0; i < 64; i++ {
		seen[codexAccountOutboundUserAgent(newCodexUAProfileTestAccount(int64(100+i), fmt.Sprintf("acct-%03d", i)))] = struct{}{}
	}
	require.GreaterOrEqual(t, len(seen), 3, "64 个账号至少应覆盖 3 种画像")
	for profileUA := range seen {
		require.NotContains(t, strings.ToLower(profileUA), "sub2api")
		require.Regexp(t, codexModernUAShape, profileUA)
	}
}

func TestCodexAccountOutboundUserAgent_PrecedenceAndGates(t *testing.T) {
	SetCodexCanonicalUserAgentResolver(nil)
	SetCodexAccountUAProfileEnabled(true)
	t.Cleanup(func() { SetCodexAccountUAProfileEnabled(true) })

	// 管理员显式配置（credentials.user_agent）优先，逐字返回
	explicit := newCodexUAProfileTestAccount(1, "acct-alpha")
	const explicitUA = "codex-tui/0.150.0 (Mac OS 26.5.0; arm64) iTerm.app/3.6.10 (codex-tui; 0.150.0)"
	explicit.Credentials["user_agent"] = explicitUA
	require.Equal(t, explicitUA, codexAccountOutboundUserAgent(explicit))

	// 无上游命名空间（无 chatgpt_account_id / seed）→ 空串，走规范 UA
	require.Equal(t, "", codexAccountOutboundUserAgent(&Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	// 非 OAuth 类账号不参与画像
	require.Equal(t, "", codexAccountOutboundUserAgent(&Account{
		ID: 8, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"chatgpt_account_id": "acct-x"},
	}))
	require.Equal(t, "", codexAccountOutboundUserAgent(nil))

	// 指纹 seed 也能作为命名空间
	seeded := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{codexFingerprintSeedExtraKey: "0f1e2d3c-4b5a-4968-8776-655443322110"}}
	require.Regexp(t, codexModernUAShape, codexAccountOutboundUserAgent(seeded))

	// 开关关闭 → 空串（显式配置仍优先）
	SetCodexAccountUAProfileEnabled(false)
	require.Equal(t, "", codexAccountOutboundUserAgent(newCodexUAProfileTestAccount(1, "acct-alpha")))
	require.Equal(t, explicitUA, codexAccountOutboundUserAgent(explicit))
}

func TestCodexAuthIdentityFromContext_FollowsAccountProfile(t *testing.T) {
	SetCodexCanonicalUserAgentResolver(nil)
	SetCodexAccountUAProfileEnabled(true)
	t.Cleanup(func() { SetCodexAccountUAProfileEnabled(true) })

	acct := newCodexUAProfileTestAccount(1, "acct-alpha")
	ctx := WithCodexAccountOutboundUserAgent(context.Background(), acct)
	ua, originator := CodexAuthIdentityFromContext(ctx)
	require.Equal(t, codexAccountOutboundUserAgent(acct), ua, "凭据面与推理面必须是同一台机器")
	require.Equal(t, "codex-tui", originator)

	// 没有账号候选（换 Token / 无命名空间账号）→ 规范身份
	canonicalUA, canonicalOriginator := CodexCanonicalAuthIdentity()
	ua, originator = CodexAuthIdentityFromContext(context.Background())
	require.Equal(t, canonicalUA, ua)
	require.Equal(t, canonicalOriginator, originator)
	ua, _ = CodexAuthIdentityFromContext(WithCodexAccountOutboundUserAgent(context.Background(), &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.Equal(t, canonicalUA, ua)
	//nolint:staticcheck // 显式验证 nil ctx 不 panic
	ua, _ = CodexAuthIdentityFromContext(nil)
	require.Equal(t, canonicalUA, ua)
}

func TestCodexCanonicalUserAgentCarriesTrailer(t *testing.T) {
	SetCodexCanonicalUserAgentResolver(nil)
	ua := CodexCanonicalUserAgent()
	require.Regexp(t, codexModernUAShape, ua)
	require.True(t, strings.HasSuffix(ua, codexUATrailer("codex-tui", CodexCanonicalClientVersion())), ua)
	require.Equal(t, "codex-tui/0.150.0 (Ubuntu 22.4.0; x86_64) xterm-256color (codex-tui; 0.150.0)", buildCodexCLIUserAgent("0.150.0"))

	// 收口重建版本时末尾组必须一起更新
	rebuilt := resolveCodexOutboundIdentity("codex-tui/0.100.0 (Windows 10.0.26200; x86_64) unknown (codex-tui; 0.100.0)")
	require.Equal(t, "codex-tui", rebuilt.originator)
	require.Equal(t, "codex-tui/"+rebuilt.version+" (Windows 10.0.26200; x86_64) unknown (codex-tui; "+rebuilt.version+")", rebuilt.userAgent)
}
