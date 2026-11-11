/**
 * Whether a click on an anchor should move to the new screen in the app,
 * instead of letting the browser load the page. The browser keeps every click
 * that means something else: a new tab, a download, another site, the API.
 * While an update waits it keeps every click, and that page load picks up the
 * new version.
 */
export function isInAppClick(
  event: Pick<MouseEvent, 'defaultPrevented' | 'button' | 'metaKey' | 'ctrlKey' | 'shiftKey' | 'altKey'>,
  anchor: Pick<HTMLAnchorElement, 'href' | 'target' | 'hasAttribute'>,
  origin: string,
  updateWaiting = false,
): boolean {
  if (updateWaiting) return false
  if (event.defaultPrevented || event.button !== 0) return false
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return false
  if (anchor.target || anchor.hasAttribute('download')) return false
  const url = new URL(anchor.href)
  if (url.origin !== origin) return false
  return url.pathname !== '/api' && !url.pathname.startsWith('/api/')
}

/**
 * Adds a history entry that marks itself as having an in-app entry before it.
 * `state` is what the screen reads back from `window.history.state`.
 */
export function pushTo(path: string, state: object = {}) {
  window.history.pushState({ ...state, inApp: true }, '', path)
}

/**
 * Moves to a screen as a new history entry, from code rather than a click on a
 * link. Back returns to the screen the user left.
 */
export function openScreen(path: string, state: object = {}) {
  pushTo(path, state)
  window.dispatchEvent(new PopStateEvent('popstate'))
  window.scrollTo(0, 0)
}

/** Whether an earlier in-app entry exists. `history.length` can't say: it counts forward entries too. */
export function canGoBack() {
  return (window.history.state as { inApp?: boolean } | null)?.inApp === true
}

/** Moves to a screen in place of the current history entry, which keeps its state. */
export function replaceTo(path: string) {
  window.history.replaceState(window.history.state, '', path)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

/**
 * Leaves a screen the way the browser's Back does. A screen opened directly,
 * with no in-app entry before it, moves to `fallback` in its place.
 */
export function goBack(fallback: string) {
  if (canGoBack()) window.history.back()
  else replaceTo(fallback)
}
