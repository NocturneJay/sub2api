import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const settingsSource = readFileSync(resolve(dir, '../SettingsView.vue'), 'utf8')
const typeSource = readFileSync(resolve(dir, '../../../types/index.ts'), 'utf8')
const zhSource = readFileSync(resolve(dir, '../../../i18n/locales/zh/admin/settings.ts'), 'utf8')
const enSource = readFileSync(resolve(dir, '../../../i18n/locales/en/admin/settings.ts'), 'utf8')

describe('custom menu open mode settings', () => {
  it('keeps the public type backward compatible', () => {
    expect(typeSource).toContain("open_mode?: 'iframe' | 'new_tab'")
  })

  it('defaults new and legacy menu items to iframe mode', () => {
    expect(settingsSource).toContain('open_mode: "iframe",')
    expect(settingsSource).toContain('open_mode: item.open_mode === "new_tab" ? "new_tab" : "iframe"')
  })

  it('renders a bound selector with both supported modes', () => {
    expect(settingsSource).toContain('v-model="item.open_mode"')
    expect(settingsSource).toContain('<option value="iframe">')
    expect(settingsSource).toContain('<option value="new_tab">')
  })

  it('provides Chinese and English labels', () => {
    for (const source of [zhSource, enSource]) {
      expect(source).toContain('openMode:')
      expect(source).toContain('openModeIframe:')
      expect(source).toContain('openModeNewTab:')
    }
  })
})
