import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const headerSource = readFileSync(resolve(dir, '../AppHeader.vue'), 'utf8')

describe('AppHeader custom-menu shortcuts', () => {
  it('renders new-tab custom menus as hardened header links', () => {
    expect(headerSource).toContain('v-if="headerCustomShortcuts.length"')
    expect(headerSource).toContain('v-for="shortcut in headerCustomShortcuts"')
    expect(headerSource).toContain('target="_blank"')
    expect(headerSource).toContain('rel="noopener noreferrer"')
    expect(headerSource).toContain('header-shortcut-label whitespace-nowrap')
    expect(headerSource).toContain('items-center gap-2 rounded-lg px-3 text-sm font-medium')
  })

  it('builds contextual URLs from the current session', () => {
    expect(headerSource).toContain(".filter((item) => item.open_mode === 'new_tab')")
    expect(headerSource).toContain('resolveCustomMenuNavigation(item, {')
    expect(headerSource).toContain('userId: authStore.user?.id')
    expect(headerSource).toContain('authToken: authStore.token')
    expect(headerSource).toContain('theme: detectTheme()')
    expect(headerSource).toContain('lang: locale.value')
    expect(headerSource).toContain('void adminSettingsStore.fetch()')
  })

  it('uses uploaded menu SVGs with a stable fallback icon', () => {
    expect(headerSource).toContain('v-html="sanitizeSvg(shortcut.iconSvg)"')
    expect(headerSource).toContain('<Icon v-else name="sparkles" size="sm" />')
    expect(headerSource).toContain('.header-shortcut-icon :deep(svg)')
  })
})
