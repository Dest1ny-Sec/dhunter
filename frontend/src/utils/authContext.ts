/**
 * Shared auth-context helpers.
 *
 * A target's stored `auth_context` is a JSON string (from PATCH
 * /targets/:id/auth) that may carry top-level cookies/headers and/or two
 * accounts (account_a / account_b) for A/B IDOR testing. Both TargetsView
 * and TargetRunsView need the same "is there actually a usable session?"
 * answer — extracted here so the logic is defined once and unit-testable.
 */

export interface AccountCtx {
  username?: string
  password?: string
  cookies?: string
  headers?: Record<string, string> | null
}

export interface AuthContext {
  cookies?: string
  headers?: Record<string, string> | null
  note?: string
  account_a?: AccountCtx
  account_b?: AccountCtx
}

/** Parse a raw auth_context (string or object) into a plain object. */
export function parseAuthContext(raw: unknown): AuthContext | null {
  if (!raw) return null
  if (typeof raw === 'string') {
    try {
      const parsed = JSON.parse(raw)
      return parsed && typeof parsed === 'object' ? (parsed as AuthContext) : null
    } catch {
      return null
    }
  }
  if (typeof raw === 'object') return raw as AuthContext
  return null
}

/** An account is usable when it has cookies, or username+password (the
 *  agent can auto-login and capture a session). */
function accountUsable(acc?: AccountCtx): boolean {
  return !!(acc && (acc.cookies || (acc.username && acc.password)))
}

/**
 * True only when the stored auth_context actually carries a usable session:
 * top-level cookies, or an account with cookies, or an account with
 * username+password. A bare `{"cookies":"","headers":null}` must NOT count —
 * that was the old bug that made the UI show "已配置会话" for nothing.
 */
export function hasUsableAuth(raw: unknown): boolean {
  const a = parseAuthContext(raw)
  if (!a) return false
  if (a.cookies) return true
  return accountUsable(a.account_a) || accountUsable(a.account_b)
}
