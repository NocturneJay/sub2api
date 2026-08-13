import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import ImageUpload from '@/components/common/ImageUpload.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))


function mountWithUrl(modelValue = '') {
  return mount(ImageUpload, {
    props: {
      modelValue,
      allowUrl: true,
      urlLabel: 'Image URL',
      urlPlaceholder: '/brand/logo.webp',
    },
  })
}

describe('ImageUpload URL mode', () => {
  it('applies a same-site image path', async () => {
    const wrapper = mountWithUrl()
    const input = wrapper.get('input[type="url"]')

    await input.setValue('  /brand/cat-magic-logo.webp  ')
    await wrapper.get('[data-testid="apply-image-url"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([
      ['/brand/cat-magic-logo.webp'],
    ])
  })

  it('normalizes and applies an HTTPS image URL', async () => {
    const wrapper = mountWithUrl()
    const input = wrapper.get('input[type="url"]')

    await input.setValue('https://cdn.example.com/logo.webp')
    await input.trigger('keydown.enter')

    expect(wrapper.emitted('update:modelValue')).toEqual([
      ['https://cdn.example.com/logo.webp'],
    ])
  })

  it('rejects unsafe or insecure URLs', async () => {
    const wrapper = mountWithUrl()
    const input = wrapper.get('input[type="url"]')

    await input.setValue('javascript:alert(1)')
    await wrapper.get('[data-testid="apply-image-url"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.text()).toContain('common.invalidImageUrl')
  })

  it('does not copy a Base64 value into the URL field', () => {
    const wrapper = mountWithUrl('data:image/png;base64,abc')

    expect((wrapper.get('input[type="url"]').element as HTMLInputElement).value).toBe('')
  })
})
