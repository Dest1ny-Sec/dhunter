import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import UiBadge from './UiBadge.vue'

describe('UiBadge', () => {
  it('applies the paused status class (regression: paused runs were unstyled)', () => {
    const w = mount(UiBadge, { props: { kind: 'status', value: 'paused' } })
    expect(w.classes()).toContain('b-paused')
  })

  it('applies the running status class', () => {
    const w = mount(UiBadge, { props: { kind: 'status', value: 'running' } })
    expect(w.classes()).toContain('b-running')
  })

  it('falls back to b-info for unknown values', () => {
    const w = mount(UiBadge, { props: { value: 'weird-state' } })
    expect(w.classes()).toContain('b-info')
  })

  it('renders the value in the default slot', () => {
    const w = mount(UiBadge, { props: { value: 'confirmed' } })
    expect(w.text()).toContain('confirmed')
  })
})
