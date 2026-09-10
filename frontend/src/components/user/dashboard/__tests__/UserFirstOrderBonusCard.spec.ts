import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UserFirstOrderBonusCard from '../UserFirstOrderBonusCard.vue'

// 卡片按钮的跳转目标必须是真实存在的路由。vue-router 在本文件里是被 mock 掉的，
// push 一个不存在的路径不会报错、只会让用户点出 404，所以直接读 router/index.ts
// 的源码把 path 字面量抓出来做包含判断（import 真 router 会拖进全部视图，太重）。
const routerSource = readFileSync(
  resolve(dirname(fileURLToPath(import.meta.url)), '../../../../router/index.ts'),
  'utf8'
)
const declaredRoutePaths = new Set(
  Array.from(routerSource.matchAll(/path:\s*'([^']+)'/g), (match) => match[1])
)

const { getAffiliateFirstOrderBonus, routerPush, publicSettings } = vi.hoisted(() => ({
  getAffiliateFirstOrderBonus: vi.fn(),
  routerPush: vi.fn(),
  publicSettings: { value: null as Record<string, unknown> | null }
}))

vi.mock('@/api/user', () => ({
  default: { getAffiliateFirstOrderBonus }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    get cachedPublicSettings() {
      return publicSettings.value
    }
  })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush })
}))

// 只覆盖 useI18n：utils/format 会 import '@/i18n'，那边需要真实的 createI18n。
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

function mountCard() {
  return mount(UserFirstOrderBonusCard, {
    global: { stubs: { Icon: true } }
  })
}

function enableFeature() {
  publicSettings.value = {
    affiliate_first_order_bonus: {
      enabled: true,
      threshold: 20,
      invitee_bonus: 10,
      inviter_rate_percent: 50,
      inviter_cap: 10,
      valid_days: 30
    }
  }
}

describe('UserFirstOrderBonusCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    publicSettings.value = null
  })

  it('renders the card and routes to the payment page when the voucher is available', async () => {
    enableFeature()
    getAffiliateFirstOrderBonus.mockResolvedValue({
      enabled: true,
      status: 'available',
      threshold: 20,
      invitee_bonus: 10,
      inviter_rate_percent: 50,
      inviter_cap: 10,
      valid_days: 30,
      expires_at: '2026-10-06T02:42:00Z'
    })

    const wrapper = mountCard()
    await flushPromises()

    expect(getAffiliateFirstOrderBonus).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('dashboard.firstOrderBonus.title')
    expect(wrapper.text()).toContain('dashboard.firstOrderBonus.expiresAt')

    await wrapper.get('button').trigger('click')
    expect(routerPush).toHaveBeenCalledWith('/purchase')
    // 目标路径必须真的在路由表里（/payment 就不在，只有 /payment/qrcode 之类的子路径）
    const pushedPath = routerPush.mock.calls[0]?.[0]
    expect(declaredRoutePaths.has(pushedPath)).toBe(true)
  })

  it('renders nothing for any status other than available', async () => {
    enableFeature()
    getAffiliateFirstOrderBonus.mockResolvedValue({
      enabled: true,
      status: 'void_below_threshold',
      threshold: 20,
      invitee_bonus: 10,
      inviter_rate_percent: 50,
      inviter_cap: 10,
      valid_days: 30
    })

    const wrapper = mountCard()
    await flushPromises()

    expect(getAffiliateFirstOrderBonus).toHaveBeenCalledTimes(1)
    expect(wrapper.html()).toBe('<!--v-if-->')
  })

  // invitee_bonus=0 是管理端合法配置（0 = 不发被邀请人那份），此时不该宣传「额外获得 $0.00」。
  it('renders nothing when the invitee bonus is configured to zero', async () => {
    enableFeature()
    getAffiliateFirstOrderBonus.mockResolvedValue({
      enabled: true,
      status: 'available',
      threshold: 20,
      invitee_bonus: 0,
      inviter_rate_percent: 50,
      inviter_cap: 10,
      valid_days: 30,
      expires_at: '2026-10-06T02:42:00Z'
    })

    const wrapper = mountCard()
    await flushPromises()

    expect(wrapper.html()).toBe('<!--v-if-->')
  })

  it('never calls the API while the public setting is off', async () => {
    publicSettings.value = { affiliate_first_order_bonus: { enabled: false } }

    const wrapper = mountCard()
    await flushPromises()

    expect(getAffiliateFirstOrderBonus).not.toHaveBeenCalled()
    expect(wrapper.html()).toBe('<!--v-if-->')
  })

  it('stays silent when the API call fails', async () => {
    enableFeature()
    getAffiliateFirstOrderBonus.mockRejectedValue(new Error('boom'))

    const wrapper = mountCard()
    await flushPromises()

    expect(wrapper.html()).toBe('<!--v-if-->')
  })
})
