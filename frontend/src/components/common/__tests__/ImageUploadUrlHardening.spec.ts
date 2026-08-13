import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import ImageUpload from '@/components/common/ImageUpload.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

function mountWithUrl(props: Record<string, unknown> = {}) {
  return mount(ImageUpload, {
    props: {
      modelValue: '',
      allowUrl: true,
      urlLabel: 'Image URL',
      urlPlaceholder: '/brand/logo.webp',
      ...props,
    },
  })
}

async function apply(wrapper: ReturnType<typeof mountWithUrl>, value: string) {
  await wrapper.get('input[type="url"]').setValue(value)
  await wrapper.get('[data-testid="apply-image-url"]').trigger('click')
}

describe('ImageUpload URL mode - rejection matrix', () => {
  it.each([
    ['protocol-relative', '//evil.example.com/logo.png'],
    ['plain http', 'http://cdn.example.com/logo.webp'],
    ['data uri typed by hand', 'data:image/png;base64,abc'],
    ['vbscript', 'vbscript:msgbox(1)'],
    ['empty', '   '],
    ['bare hostname', 'cdn.example.com/logo.webp'],
  ])('rejects %s', async (_label, value) => {
    const wrapper = mountWithUrl()
    await apply(wrapper, value)

    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.text()).toContain('common.invalidImageUrl')
  })

  it('clears a stale error after a subsequent valid value', async () => {
    const wrapper = mountWithUrl()
    await apply(wrapper, 'javascript:alert(1)')
    expect(wrapper.text()).toContain('common.invalidImageUrl')

    await apply(wrapper, '/brand/cat-magic-logo.webp')
    expect(wrapper.text()).not.toContain('common.invalidImageUrl')
    expect(wrapper.emitted('update:modelValue')).toEqual([['/brand/cat-magic-logo.webp']])
  })
})

describe('ImageUpload URL mode - visibility and sync', () => {
  it('hides the URL field when allowUrl is false', () => {
    const wrapper = mountWithUrl({ allowUrl: false })
    expect(wrapper.find('input[type="url"]').exists()).toBe(false)
  })

  it('hides the URL field in svg mode even when allowUrl is true', () => {
    const wrapper = mountWithUrl({ mode: 'svg' })
    expect(wrapper.find('input[type="url"]').exists()).toBe(false)
  })

  it('clears the URL field when the parent switches to a Base64 value', async () => {
    const wrapper = mountWithUrl({ modelValue: '/brand/cat-magic-logo.webp' })
    expect((wrapper.get('input[type="url"]').element as HTMLInputElement).value)
      .toBe('/brand/cat-magic-logo.webp')

    await wrapper.setProps({ modelValue: 'data:image/png;base64,abc' })
    expect((wrapper.get('input[type="url"]').element as HTMLInputElement).value).toBe('')
  })

  it('reflects a path pushed down from the parent', async () => {
    const wrapper = mountWithUrl()
    await wrapper.setProps({ modelValue: '/brand/other.webp' })
    expect((wrapper.get('input[type="url"]').element as HTMLInputElement).value)
      .toBe('/brand/other.webp')
  })
})
