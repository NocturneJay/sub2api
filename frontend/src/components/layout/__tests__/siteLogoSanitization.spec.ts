import { existsSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const sidebarSource = readFileSync(resolve(dir, '../AppSidebar.vue'), 'utf8')
const homeViewSource = readFileSync(resolve(dir, '../../../views/HomeView.vue'), 'utf8')
const keyUsageViewSource = readFileSync(resolve(dir, '../../../views/KeyUsageView.vue'), 'utf8')
const settingsViewSource = readFileSync(resolve(dir, '../../../views/admin/SettingsView.vue'), 'utf8')

describe('site_logo sanitization', () => {
  it('AppSidebar uses the dedicated Cat Magic brand assets', () => {
    expect(sidebarSource).toContain('src="/brand/cat-magic-logo.webp"')
    expect(sidebarSource).toContain('src="/brand/cat-magic-wordmark.webp"')
    expect(sidebarSource).toContain('src="/brand/cat-magic-wordmark-dark.webp"')
    expect(existsSync(resolve(dir, '../../../../public/brand/cat-magic-logo.webp'))).toBe(true)
    expect(existsSync(resolve(dir, '../../../../public/brand/cat-magic-wordmark.webp'))).toBe(true)
    expect(existsSync(resolve(dir, '../../../../public/brand/cat-magic-wordmark-dark.webp'))).toBe(true)
    // 反向断言：侧栏改用内置素材后不再消费 site_logo。若将来有人把
    // :src="appStore.siteLogo" 加回来却忘了套 sanitizeUrl，这里会红。
    expect(sidebarSource).not.toContain('appStore.siteLogo')
  })

  it('HomeView applies sanitizeUrl to siteLogo', () => {
    expect(homeViewSource).toContain('sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo')
  })

  it('KeyUsageView applies sanitizeUrl to siteLogo', () => {
    expect(keyUsageViewSource).toContain('sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo')
  })

  it('configurable logo consumers allow relative and data URLs', () => {
    for (const src of [homeViewSource, keyUsageViewSource]) {
      expect(src).toContain('allowRelative: true')
      expect(src).toContain('allowDataUrl: true')
    }
  })

  it('wires the site logo uploader to safe URL mode', () => {
    expect(settingsViewSource).toContain('v-model="form.site_logo"')
    expect(settingsViewSource).toContain('allow-url')
    expect(settingsViewSource).toContain("t('admin.settings.site.logoUrlPlaceholder')")
  })
})
