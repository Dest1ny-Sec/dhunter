import { describe, expect, it } from 'vitest'
import { isIMEComposing } from './ime'

function keyEvent(partial: Partial<KeyboardEvent>): KeyboardEvent {
  return { isComposing: false, keyCode: 0, ...partial } as KeyboardEvent
}

describe('isIMEComposing', () => {
  it('returns true while an IME composition is in progress', () => {
    expect(isIMEComposing(keyEvent({ isComposing: true }))).toBe(true)
  })

  it('returns true for the legacy IME keyCode 229', () => {
    expect(isIMEComposing(keyEvent({ isComposing: false, keyCode: 229 }))).toBe(true)
  })

  it('returns false for a normal Enter', () => {
    expect(isIMEComposing(keyEvent({ isComposing: false, keyCode: 13 }))).toBe(false)
  })
})
