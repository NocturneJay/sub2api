import { describe, expect, it } from 'vitest'

import { resolveCustomMenuNavigation } from '../custom-menu'

describe('resolveCustomMenuNavigation', () => {
  it('keeps legacy and explicit iframe items on the internal custom route', () => {
    expect(resolveCustomMenuNavigation({ id: 'legacy', url: 'https://example.com' })).toEqual({
      path: '/custom/legacy',
      openInNewTab: false,
    })
    expect(resolveCustomMenuNavigation({ id: 'iframe', open_mode: 'iframe', url: 'https://example.com' })).toEqual({
      path: '/custom/iframe',
      openInNewTab: false,
    })
  })

  it('builds a sanitized new-tab URL with the same user context as iframe pages', () => {
    const result = resolveCustomMenuNavigation(
      {
        id: 'image2',
        open_mode: 'new_tab',
        url: '  https://image.example.com/generate  ',
      },
      {
        userId: 42,
        authToken: 'test-token',
        theme: 'dark',
        lang: 'zh-CN',
      },
    )

    expect(result.path).toBe('/custom/image2')
    expect(result.openInNewTab).toBe(true)
    const url = new URL(result.externalUrl ?? '')
    expect(url.origin + url.pathname).toBe('https://image.example.com/generate')
    expect(url.searchParams.get('user_id')).toBe('42')
    expect(url.searchParams.get('token')).toBe('test-token')
    expect(url.searchParams.get('theme')).toBe('dark')
    expect(url.searchParams.get('lang')).toBe('zh-CN')
    expect(url.searchParams.get('ui_mode')).toBe('embedded')
  })

  it('does not fall back to the internal route when a new-tab URL is unsafe', () => {
    expect(resolveCustomMenuNavigation({
      id: 'unsafe',
      open_mode: 'new_tab',
      url: 'javascript:alert(1)',
    })).toEqual({
      path: '/custom/unsafe',
      openInNewTab: true,
      externalUrl: '',
    })
  })
})
