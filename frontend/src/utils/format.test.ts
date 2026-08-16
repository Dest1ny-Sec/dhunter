import { describe, expect, it } from 'vitest'
import { clip, escapeHtml } from './format'

describe('escapeHtml', () => {
  it('escapes HTML metacharacters', () => {
    expect(escapeHtml(`<img src=x onerror="alert(1)"> & '`)).toBe(
      '&lt;img src=x onerror=&quot;alert(1)&quot;&gt; &amp; &#39;',
    )
  })
  it('leaves plain text alone', () => {
    expect(escapeHtml('hello 世界')).toBe('hello 世界')
  })
})

describe('clip', () => {
  it('truncates long strings with an ellipsis', () => {
    expect(clip('abcdefghij', 5)).toBe('abcde…')
  })
  it('keeps short strings unchanged', () => {
    expect(clip('abc', 5)).toBe('abc')
  })
})
