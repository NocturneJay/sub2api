const AFFILIATE_DEVICE_ID_KEY = 'affiliate_device_id'

function normalizeDeviceID(value: unknown): string {
  return typeof value === 'string' && value.length >= 8 && value.length <= 128 ? value : ''
}

function createRandomDeviceID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `rnd-${crypto.randomUUID()}`
  }

  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    const bytes = new Uint8Array(16)
    crypto.getRandomValues(bytes)
    return `rnd-${Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')}`
  }

  return `rnd-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

function stableHash(input: string): string {
  let h1 = 0xdeadbeef ^ input.length
  let h2 = 0x41c6ce57 ^ input.length

  for (let i = 0; i < input.length; i += 1) {
    const ch = input.charCodeAt(i)
    h1 = Math.imul(h1 ^ ch, 2654435761)
    h2 = Math.imul(h2 ^ ch, 1597334677)
  }

  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909)
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909)

  return (4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(36)
}

function getTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}

function createFingerprintDeviceID(): string {
  if (typeof window === 'undefined') {
    return ''
  }

  const nav = window.navigator
  const screen = window.screen
  const navWithMemory = nav as Navigator & { deviceMemory?: number }
  const parts = [
    `ua=${nav.userAgent || ''}`,
    `platform=${nav.platform || ''}`,
    `vendor=${nav.vendor || ''}`,
    `languages=${Array.isArray(nav.languages) ? nav.languages.join(',') : nav.language || ''}`,
    `timezone=${getTimezone()}`,
    `screen=${screen?.width || 0}x${screen?.height || 0}x${screen?.availWidth || 0}x${screen?.availHeight || 0}`,
    `depth=${screen?.colorDepth || 0}x${screen?.pixelDepth || 0}`,
    `dpr=${window.devicePixelRatio || 0}`,
    `cpu=${nav.hardwareConcurrency || 0}`,
    `memory=${navWithMemory.deviceMemory || 0}`,
    `touch=${nav.maxTouchPoints || 0}`,
    `cookies=${nav.cookieEnabled ? 1 : 0}`,
  ]
  const material = parts.join('|')

  return material.length >= 24 ? `fp2-${stableHash(material).padStart(16, '0')}` : ''
}

function persistDeviceID(deviceID: string): void {
  try {
    window.localStorage.setItem(AFFILIATE_DEVICE_ID_KEY, deviceID)
  } catch {
    // Storage can be unavailable in private contexts; the caller can still use the in-memory value.
  }
}

export function getAffiliateDeviceID(): string {
  if (typeof window === 'undefined') {
    return ''
  }

  const fingerprintID = createFingerprintDeviceID()
  if (fingerprintID) {
    persistDeviceID(fingerprintID)
    return fingerprintID
  }

  try {
    const existing = normalizeDeviceID(window.localStorage.getItem(AFFILIATE_DEVICE_ID_KEY))
    if (existing) {
      return existing
    }

    const next = createRandomDeviceID()
    persistDeviceID(next)
    return next
  } catch {
    return createRandomDeviceID()
  }
}

export function affiliateDevicePayload(): { affiliate_device_id?: string } {
  const deviceID = getAffiliateDeviceID()
  return deviceID ? { affiliate_device_id: deviceID } : {}
}

export function affiliateDeviceHeaders(): Record<string, string> {
  const deviceID = getAffiliateDeviceID()
  return deviceID ? { 'X-Affiliate-Device-ID': deviceID } : {}
}
