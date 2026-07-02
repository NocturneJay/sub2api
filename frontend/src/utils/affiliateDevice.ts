const AFFILIATE_DEVICE_ID_KEY = 'affiliate_device_id'

function normalizeDeviceID(value: unknown): string {
  return typeof value === 'string' && value.length >= 8 && value.length <= 128 ? value : ''
}

function createDeviceID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }

  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    const bytes = new Uint8Array(16)
    crypto.getRandomValues(bytes)
    return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
  }

  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

export function getAffiliateDeviceID(): string {
  if (typeof window === 'undefined') {
    return ''
  }

  try {
    const existing = normalizeDeviceID(window.localStorage.getItem(AFFILIATE_DEVICE_ID_KEY))
    if (existing) {
      return existing
    }

    const next = createDeviceID()
    window.localStorage.setItem(AFFILIATE_DEVICE_ID_KEY, next)
    return next
  } catch {
    return ''
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
