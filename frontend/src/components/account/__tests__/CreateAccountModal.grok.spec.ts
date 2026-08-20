import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(
  resolve(process.cwd(), 'src/components/account/CreateAccountModal.vue'),
  'utf8'
)

describe('CreateAccountModal Grok account types', () => {
  it('offers API-key setup alongside OAuth with the official xAI default', () => {
    expect(source).toContain('data-testid="grok-account-type-api-key"')
    expect(source).toContain("@click=\"accountCategory = 'apikey'\"")
    expect(source).toContain("newPlatform === 'grok'")
    expect(source).toContain("? 'https://api.x.ai/v1'")
    expect(source).toContain("form.platform === 'grok'")
    // ⚠️ 上游 v0.1.178 把 apiKeyValuePlaceholder 从三元链重构成 switch（为了加
    // kimi/zhipu/deepseek），却没同步改这条断言；上游在 v0.1.179 的
    // f917d19d3 自己修成了下面这条「模板绑定」断言。两条都留：
    // 绑定断言守「computed 确实接到了模板上」，switch 断言守「grok 分支还在」。
    expect(source).toContain(':placeholder="apiKeyValuePlaceholder"')
    expect(source).toContain("case 'grok':")
    expect(source).toContain("return 'xai-...'")
  })

  it('exposes custom upstream URL and header override for the OAuth create flow', () => {
    expect(source).toContain('data-testid="grok-custom-base-url-toggle"')
    expect(source).toContain('data-testid="grok-custom-base-url-input"')
    expect(source).toContain('form.platform === \'grok\' && isOAuthFlow')
  })

  it('validates and applies upstream config on Grok OAuth create paths', () => {
    // 授权码兑换 / RT 批量 / SSO 批量（密码授权已隐藏）
    expect(source.match(/validateGrokOAuthUpstreamConfig\(\)/g)?.length).toBeGreaterThanOrEqual(3)
    expect(source.match(/applyGrokOAuthUpstreamConfig\(credentials\)/g)?.length).toBeGreaterThanOrEqual(3)
  })

  it('hides Grok password authorize option in the create flow', () => {
    expect(source).toContain(':show-email-password-option="false"')
  })
})
