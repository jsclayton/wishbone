import { isInAppClick, replaceTo } from './navigation'

const origin = window.location.origin

function anchor(href: string, attrs: Record<string, string> = {}) {
  const a = document.createElement('a')
  a.setAttribute('href', href)
  for (const [k, v] of Object.entries(attrs)) a.setAttribute(k, v)
  return a
}

function click(init: Partial<MouseEventInit> = {}) {
  return new MouseEvent('click', { button: 0, cancelable: true, ...init })
}

function inApp(href: string, attrs: Record<string, string> = {}, init: Partial<MouseEventInit> = {}) {
  const a = anchor(href, attrs)
  return isInAppClick(click(init), a, origin)
}

test.each(['/list', '/', '/people/3', '/claims#claim-9', '/list?x=1'])('a plain click on %s stays in the app', (href) => {
  expect(inApp(href)).toBe(true)
})

test('an absolute link to the same origin stays in the app', () => {
  expect(inApp(`${origin}/groups`)).toBe(true)
})

test.each([
  ['a middle click', { button: 1 }],
  ['a right click', { button: 2 }],
  ['Meta', { metaKey: true }],
  ['Ctrl', { ctrlKey: true }],
  ['Shift', { shiftKey: true }],
  ['Alt', { altKey: true }],
])('%s is left to the browser', (_name, init) => {
  expect(inApp('/list', {}, init)).toBe(false)
})

test('a click that something already handled is left alone', () => {
  const a = anchor('/list')
  const e = click()
  e.preventDefault()
  expect(isInAppClick(e, a, origin)).toBe(false)
})

test.each([
  ['target', { target: '_blank' }],
  ['download', { download: '' }],
])('an anchor with %s is left to the browser', (_name, attrs) => {
  expect(inApp('/list', attrs)).toBe(false)
})

test('another origin is left to the browser', () => {
  expect(inApp('https://example.com/list')).toBe(false)
})

test.each(['/api/v2/users/me', '/api/'])('%s is left to the browser', (href) => {
  expect(inApp(href)).toBe(false)
})

test('a path that only starts like /api stays in the app', () => {
  expect(inApp('/apiary')).toBe(true)
})

test('while an update is waiting, a link is an ordinary page load', () => {
  expect(isInAppClick(click(), anchor('/list'), origin, true)).toBe(false)
})

test('replaceTo keeps the state of the entry it replaces', () => {
  window.history.replaceState({ inApp: true }, '', '/people/7')
  replaceTo('/list')
  expect(window.location.pathname).toBe('/list')
  expect(window.history.state).toEqual({ inApp: true })
  window.history.replaceState(null, '', '/')
})
