import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import RedeemView from '../RedeemView.vue'

const {
  appState,
  fetchPublicSettings,
  getHistory,
  redeem,
  refreshUser,
  fetchActiveSubscriptions,
} = vi.hoisted(() => ({
  appState: {
    cachedPublicSettings: null as Record<string, unknown> | null,
    fetchPublicSettings: vi.fn(),
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  },
  fetchPublicSettings: vi.fn(),
  getHistory: vi.fn(),
  redeem: vi.fn(),
  refreshUser: vi.fn(),
  fetchActiveSubscriptions: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appState,
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      id: 42,
      balance: 23.23,
      concurrency: 50,
    },
    refreshUser,
  }),
}))

vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({ fetchActiveSubscriptions }),
}))

vi.mock('@/api', () => ({
  redeemAPI: { getHistory, redeem },
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

function mountView() {
  return mount(RedeemView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Icon: { template: '<svg />' },
      },
    },
  })
}

describe('user RedeemView purchase link', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    appState.cachedPublicSettings = {
      contact_info: '',
      redeem_purchase_url: '',
    }
    appState.fetchPublicSettings = fetchPublicSettings
    fetchPublicSettings.mockResolvedValue(appState.cachedPublicSettings)
    getHistory.mockResolvedValue([])
  })

  it('renders a safe standalone HTTPS link without account context', async () => {
    appState.cachedPublicSettings = {
      contact_info: '',
      redeem_purchase_url: 'https://shop.example.com/redeem?campaign=summer',
    }

    const wrapper = mountView()
    await flushPromises()

    const link = wrapper.get('[data-testid="redeem-purchase-link"]')
    expect(link.text()).toContain('redeem.purchaseCode')
    expect(link.attributes('href')).toBe('https://shop.example.com/redeem?campaign=summer')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toBe('noopener noreferrer')
    expect(link.attributes('href')).not.toContain('token')
    expect(link.attributes('href')).not.toContain('user_id')
    expect(link.classes()).toEqual(expect.arrayContaining(['w-full', 'sm:w-auto']))
  })

  it.each(['', 'http://shop.example.com/redeem', 'javascript:alert(1)'])(
    'hides the purchase action for an empty or unsafe URL: %s',
    async (redeemPurchaseUrl) => {
      appState.cachedPublicSettings = {
        contact_info: '',
        redeem_purchase_url: redeemPurchaseUrl,
      }

      const wrapper = mountView()
      await flushPromises()

      expect(wrapper.find('[data-testid="redeem-purchase-link"]').exists()).toBe(false)
    },
  )
})
