import { describe, expect, it } from 'vitest'
import { hasUsableAuth, parseAuthContext } from './authContext'

describe('hasUsableAuth', () => {
  it('rejects empty / absent contexts', () => {
    expect(hasUsableAuth(null)).toBe(false)
    expect(hasUsableAuth('')).toBe(false)
    expect(hasUsableAuth('{}')).toBe(false)
    expect(hasUsableAuth('not json')).toBe(false)
  })

  it('REJECTS a bare cookies:"",headers:null payload — the old false-positive', () => {
    // This is exactly what PATCH /targets/:id/auth stored when the user
    // only filled account_a/account_b before the backend supported them.
    expect(hasUsableAuth('{"cookies":"","headers":null,"note":""}')).toBe(false)
  })

  it('accepts top-level cookies', () => {
    expect(hasUsableAuth('{"cookies":"SESS=abc"}')).toBe(true)
  })

  it('accepts an account with cookies', () => {
    expect(hasUsableAuth(JSON.stringify({ account_a: { username: 'a', cookies: 'SESS=a' } }))).toBe(true)
  })

  it('accepts an account with username+password (auto-login possible)', () => {
    expect(hasUsableAuth(JSON.stringify({ account_a: { username: 'a', password: 'pw' } }))).toBe(true)
  })

  it('REJECTS an account with only a username (no way to log in)', () => {
    expect(hasUsableAuth(JSON.stringify({ account_a: { username: 'a' } }))).toBe(false)
  })

  it('accepts account_b even when account_a is empty', () => {
    expect(hasUsableAuth(JSON.stringify({ account_b: { username: 'b', password: 'pw' } }))).toBe(true)
  })

  it('accepts a pre-parsed object too', () => {
    expect(hasUsableAuth({ cookies: 'SESS=x' })).toBe(true)
  })
})

describe('parseAuthContext', () => {
  it('returns null for unparseable input', () => {
    expect(parseAuthContext('{broken')).toBeNull()
    expect(parseAuthContext('42')).toBeNull()
  })
  it('parses a JSON string into an object', () => {
    expect(parseAuthContext('{"cookies":"a"}')?.cookies).toBe('a')
  })
})
