/**
 * Browser notification helpers (run-completion alerts). Falls back
 * silently when the Notification API is unavailable or permission was
 * denied — a nice-to-have, never a blocker.
 */

/** Ask for permission once, from a user gesture (e.g. opening a run page). */
export function requestNotifyPermission(): void {
  if (typeof window === 'undefined' || !('Notification' in window)) return
  if (Notification.permission === 'default') {
    try {
      void Notification.requestPermission()
    } catch {
      /* ignore */
    }
  }
}

/** Fire a desktop notification if allowed; otherwise do nothing. */
export function notify(title: string, body?: string): void {
  if (typeof window === 'undefined' || !('Notification' in window)) return
  if (Notification.permission !== 'granted') return
  try {
    // `tag` collapses duplicate notifications for the same run.
    new Notification(title, { body, tag: 'dhunter-run', icon: '/logo-512.png' })
  } catch {
    /* ignore */
  }
}
