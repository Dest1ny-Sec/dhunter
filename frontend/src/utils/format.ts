/** Escape a string for safe HTML text insertion (used in fallback paths
 *  and generated report files). */
export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!)
}

/** Keep a string under `max` chars, appending an ellipsis. */
export function clip(s: string, max: number): string {
  return s.length > max ? s.slice(0, max) + '…' : s
}
