/**
 * IME (Chinese input method) safe Enter handling.
 *
 * When a user types with a Chinese IME, pressing Enter to confirm a
 * candidate ALSO fires a keyup/keydown Enter. Naively binding `@keyup.enter`
 * would send the (half-typed) input prematurely. Guard with this: it returns
 * true when the Enter is part of IME composition, in which case the handler
 * should NOT fire.
 */
export function isIMEComposing(e: KeyboardEvent): boolean {
  // isComposing is the modern signal; keyCode 229 is the legacy IME key code.
  return e.isComposing === true || e.keyCode === 229
}

/** Wrap a handler so it only fires on a "real" Enter, not IME composition. */
export function onEnter(handler: () => void) {
  return (e: KeyboardEvent) => {
    if (isIMEComposing(e)) return
    handler()
  }
}
