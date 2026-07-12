import type { CustomMenuItem } from '@/types'
import { buildEmbeddedUrl } from '@/utils/embedded-url'
import { sanitizeUrl } from '@/utils/url'

export interface CustomMenuNavigation {
  path: string
  openInNewTab: boolean
  externalUrl?: string
}

export interface CustomMenuNavigationContext {
  userId?: number
  authToken?: string | null
  theme?: 'light' | 'dark'
  lang?: string
}

export function resolveCustomMenuNavigation(
  item: Pick<CustomMenuItem, 'id' | 'open_mode' | 'url'>,
  context: CustomMenuNavigationContext = {},
): CustomMenuNavigation {
  const path = `/custom/${item.id}`
  if (item.open_mode !== 'new_tab') {
    return { path, openInNewTab: false }
  }

  return {
    path,
    openInNewTab: true,
    externalUrl: sanitizeUrl(
      buildEmbeddedUrl(
        item.url,
        context.userId,
        context.authToken,
        context.theme,
        context.lang,
      ),
    ),
  }
}
