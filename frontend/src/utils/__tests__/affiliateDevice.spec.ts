import { beforeEach, describe, expect, it } from 'vitest'
import { affiliateDeviceHeaders, affiliateDevicePayload, getAffiliateDeviceID } from '@/utils/affiliateDevice'

describe('affiliateDevice', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('keeps the same fingerprint when browser storage is cleared', () => {
    const first = getAffiliateDeviceID()
    localStorage.clear()
    const second = getAffiliateDeviceID()

    expect(first).toMatch(/^fp2-/)
    expect(second).toBe(first)
  })

  it('prefers the browser fingerprint over an older random stored id', () => {
    localStorage.setItem('affiliate_device_id', 'test-device-id')

    const deviceID = getAffiliateDeviceID()

    expect(deviceID).toMatch(/^fp2-/)
    expect(deviceID).not.toBe('test-device-id')
  })

  it('uses the fingerprint in affiliate payloads and headers', () => {
    const deviceID = getAffiliateDeviceID()

    expect(affiliateDevicePayload()).toEqual({ affiliate_device_id: deviceID })
    expect(affiliateDeviceHeaders()).toEqual({ 'X-Affiliate-Device-ID': deviceID })
  })
})
