import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ModelIcon from '../ModelIcon.vue'

describe('ModelIcon', () => {
  it.each(['Nano-Banana-2', 'Nano-Banana-2-Lite'])('uses the Gemini icon for %s', (model) => {
    const wrapper = mount(ModelIcon, { props: { model } })

    expect(wrapper.find('svg.model-icon').exists()).toBe(true)
    expect(wrapper.find('.model-icon-fallback').exists()).toBe(false)
    expect(wrapper.find('path').attributes('fill')).toBe('#4285F4')
  })

  it('keeps the initial fallback for unknown aliases', () => {
    const wrapper = mount(ModelIcon, { props: { model: 'Unknown-Alias' } })

    expect(wrapper.find('svg.model-icon').exists()).toBe(false)
    expect(wrapper.get('.model-icon-fallback').text()).toBe('U')
  })
})
