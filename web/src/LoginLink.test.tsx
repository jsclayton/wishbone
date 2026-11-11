import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { App } from './App'

type Reply = { status: number; body?: unknown } | 'offline'

const expires = '2030-11-07T15:40:00'
const valid: Reply = { status: 200, body: { expires_at: expires, for_current_user: false } }
const own: Reply = { status: 200, body: { expires_at: expires, for_current_user: true } }
const person = (id: string, name: string) => ({
  id,
  name,
  last_name: null,
  birthday: { month: 1, day: 1, year: null },
  is_admin: false,
  profile_confirmed_at: null,
  created_at: '2026-11-08T12:00:00Z',
  can_make_login_link: true,
  can_manage_sessions: true,
  parents: [],
  children: [],
})
const john = person('7', 'John')
const maya = person('8', 'Maya')
const noSession = { status: 401, body: { status: 401, code: 'unauthorized', detail: 'Sign in to do this.' } }
const badLink = { status: 401, body: { status: 401, code: 'login_link_invalid', detail: 'This login link is invalid or has expired.' } }

function mockFetch(routes: Record<string, Reply | (() => Reply | Promise<Reply>)>) {
  const fn = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(async (url, init) => {
    const key = `${init?.method ?? 'GET'} ${url}`
    const route = routes[key]
    if (!route) throw new Error(`unexpected request: ${key}`)
    const r = typeof route === 'function' ? await route() : route
    if (r === 'offline') throw new TypeError('Failed to fetch')
    return new Response(r.body === undefined ? null : JSON.stringify(r.body), { status: r.status })
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

const calls = (fn: ReturnType<typeof mockFetch>, url: string) => fn.mock.calls.filter(([u]) => u === url).length
const CHECK = 'POST /api/v2/login-links/check'
const REDEEM = 'POST /api/v2/login-links/redeem'
const ME = 'GET /api/v2/users/me'
const SETUP = 'GET /api/v2/setup'

function open(path = '/login/abc') {
  window.history.replaceState(null, '', path)
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  return render(<App />)
}

const heading = (name: string) => screen.findByRole('heading', { level: 1, name })

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

test('the page shows the expiry above Sign in and redeems only on the tap', async () => {
  const fn = mockFetch({ [CHECK]: valid, [ME]: noSession })
  open()

  const button = await screen.findByRole('button', { name: 'Sign in' })
  expect(screen.getByRole('heading', { level: 1, name: 'Wishbone' })).toBeInTheDocument()
  expect(screen.getByText('Tap the button to sign in. This link works once.')).toBeInTheDocument()
  const line = screen.getByText(/^Thu Nov 7, 3:40\sPM$/).closest('p')!
  expect(line.textContent).toMatch(/^Expires Thu Nov 7, 3:40\sPM$/)
  expect(line.compareDocumentPosition(button) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

  expect(calls(fn, '/api/v2/login-links/redeem')).toBe(0)
  const init = fn.mock.calls.find(([u]) => u === '/api/v2/login-links/check')?.[1]
  expect(JSON.parse(init?.body as string)).toEqual({ token: 'abc' })
})

test('the web app redeems with the cookie, and never asks for a bearer token', async () => {
  const fn = mockFetch({
    [CHECK]: valid,
    [ME]: noSession,
    [REDEEM]: { status: 200, body: { name: 'Maya', initials: 'M', replaced_session: false, switched_user: false } },
  })
  open()
  await userEvent.click(await screen.findByRole('button', { name: 'Sign in' }))
  await waitFor(() => expect(calls(fn, '/api/v2/login-links/redeem')).toBe(1))
  const init = fn.mock.calls.find(([u]) => u === '/api/v2/login-links/redeem')?.[1]
  expect(JSON.parse(init?.body as string)).toEqual({ token: 'abc', session: 'cookie' })
})

test('signing in with a good link goes Home with no banner', async () => {
  let me: Reply = noSession
  mockFetch({
    [CHECK]: valid,
    [ME]: () => me,
    [REDEEM]: () => {
      me = { status: 200, body: maya }
      return { status: 200, body: { name: 'Maya', initials: 'M', replaced_session: false, switched_user: false } }
    },
  })
  open()
  await userEvent.click(await screen.findByRole('button', { name: 'Sign in' }))
  expect(await heading('Home')).toBeInTheDocument()
  expect(window.location.pathname).toBe('/')
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

test('a link for the user already signed in shows the normal page and no banner', async () => {
  const fn = mockFetch({
    [CHECK]: own,
    [ME]: { status: 200, body: maya },
    [REDEEM]: { status: 200, body: { name: 'Maya', initials: 'M', replaced_session: true, switched_user: false } },
  })
  open()
  await userEvent.click(await screen.findByRole('button', { name: 'Sign in' }))
  expect(await heading('Home')).toBeInTheDocument()
  expect(calls(fn, '/api/v2/login-links/redeem')).toBe(1)
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

describe('a link for another user', () => {
  const routes = { [CHECK]: valid, [ME]: { status: 200, body: john } }

  test('warns first, with no top row and no sign-in button that skips it', async () => {
    const fn = mockFetch(routes)
    open()
    expect(await heading('Signed in as John')).toBeInTheDocument()
    expect(
      screen.getByText('This link is for a different user. If you use it, John is signed out on this browser and needs a login link to get back in.'),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Sign in' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Menu' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Wishbone' })).not.toBeInTheDocument()
    expect(calls(fn, '/api/v2/login-links/redeem')).toBe(0)
  })

  test('Stay signed in goes Home and leaves the link unused, and Back returns to the warning', async () => {
    const fn = mockFetch(routes)
    open()
    await heading('Signed in as John')
    await userEvent.click(screen.getByRole('link', { name: 'Stay signed in as John' }))
    expect(await heading('Home')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/')
    expect(calls(fn, '/api/v2/login-links/redeem')).toBe(0)

    act(() => window.history.back())
    expect(await heading('Signed in as John')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/login/abc')
  })

  test('going on signs in and shows the one-time banner', async () => {
    mockFetch({
      ...routes,
      [REDEEM]: { status: 200, body: { name: 'Maya', initials: 'M', replaced_session: true, switched_user: true } },
    })
    open()
    await heading('Signed in as John')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in with this link' }))
    expect(await screen.findByRole('status')).toHaveTextContent("You're now signed in as Maya.")
    expect(window.location.pathname).toBe('/')
  })

  test('going on after Back draws no Home for the old user', async () => {
    let answer: (r: Reply) => void = () => {}
    let me: Reply | Promise<Reply> = { status: 200, body: john }
    mockFetch({
      ...routes,
      [ME]: () => me,
      [REDEEM]: () => {
        me = new Promise<Reply>((resolve) => (answer = resolve))
        return { status: 200, body: { name: 'Maya', initials: 'M', replaced_session: true, switched_user: true } }
      },
    })
    open()
    await userEvent.click(await screen.findByRole('link', { name: 'Stay signed in as John' }))
    await heading('Home')
    act(() => window.history.back())
    await userEvent.click(await screen.findByRole('button', { name: 'Sign in with this link' }))

    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(screen.queryByText('Welcome, John.')).not.toBeInTheDocument()
    expect(screen.queryByRole('status')).not.toBeInTheDocument()

    answer({ status: 200, body: maya })
    expect(await screen.findByRole('status')).toHaveTextContent("You're now signed in as Maya.")
  })

  test('a link someone else used in the meantime shows LinkProblem', async () => {
    mockFetch({ ...routes, [REDEEM]: badLink })
    open()
    await heading('Signed in as John')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in with this link' }))
    expect(await heading("This link doesn't work")).toBeInTheDocument()
  })
})

describe('the tap looks again before it redeems', () => {
  test('another user signed in since the page opened gets the warning, and the link stays unused', async () => {
    let me: Reply = noSession
    const fn = mockFetch({
      [CHECK]: valid,
      [ME]: () => me,
      [REDEEM]: { status: 200, body: { name: 'Maya', initials: 'M', replaced_session: true, switched_user: true } },
    })
    open()
    const button = await screen.findByRole('button', { name: 'Sign in' })

    me = { status: 200, body: john }
    await userEvent.click(button)

    expect(await heading('Signed in as John')).toBeInTheDocument()
    expect(document.activeElement).toBe(screen.getByRole('main'))
    expect(calls(fn, '/api/v2/login-links/redeem')).toBe(0)
  })

  test('a link that stopped working shows LinkProblem without trying to redeem', async () => {
    let check: Reply = valid
    const fn = mockFetch({ [CHECK]: () => check, [ME]: noSession })
    open()
    const button = await screen.findByRole('button', { name: 'Sign in' })

    check = badLink
    await userEvent.click(button)

    expect(await heading("This link doesn't work")).toBeInTheDocument()
    expect(calls(fn, '/api/v2/login-links/redeem')).toBe(0)
  })

  test.each<{ name: string; check: Reply; me: Reply; page: string }>([
    { name: 'the check answers 5xx', check: { status: 503 }, me: noSession, page: "Can't reach Wishbone" },
    { name: 'the check gets no answer', check: 'offline', me: noSession, page: "Can't reach Wishbone" },
    { name: 'the check answers 429', check: { status: 429 }, me: noSession, page: 'Too many tries' },
    { name: 'the check is good and /users/me answers 5xx', check: valid, me: { status: 503 }, page: "Can't reach Wishbone" },
    { name: 'the check is good and /users/me gets no answer', check: valid, me: 'offline', page: "Can't reach Wishbone" },
  ])('fails closed when $name: $page shows and redeem is never called', async ({ check, me, page }) => {
    let nextCheck: Reply = valid
    let nextMe: Reply = noSession
    const fn = mockFetch({
      [CHECK]: () => nextCheck,
      [ME]: () => nextMe,
      [REDEEM]: { status: 200, body: { name: 'Maya', initials: 'M', replaced_session: false, switched_user: false } },
    })
    open()
    const button = await screen.findByRole('button', { name: 'Sign in' })

    nextCheck = check
    nextMe = me
    await userEvent.click(button)

    expect(await heading(page)).toBeInTheDocument()
    expect(calls(fn, '/api/v2/login-links/redeem')).toBe(0)
  })

  test('the warning page redeems straight away: the person has just agreed', async () => {
    const fn = mockFetch({
      [CHECK]: valid,
      [ME]: { status: 200, body: john },
      [REDEEM]: { status: 200, body: { name: 'Maya', initials: 'M', replaced_session: true, switched_user: true } },
    })
    open()
    await heading('Signed in as John')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in with this link' }))
    await screen.findByRole('status')
    expect(calls(fn, '/api/v2/login-links/check')).toBe(1)
  })
})

test('a bad link shows LinkProblem, whose button goes to Wishbone', async () => {
  const fn = mockFetch({ [CHECK]: badLink, [ME]: noSession, [SETUP]: { status: 200, body: { is_needed: false } } })
  open()
  expect(await heading("This link doesn't work")).toBeInTheDocument()
  expect(screen.getByText('It may have expired or already been used. Ask the person who sent it for a new one.')).toBeInTheDocument()
  expect(calls(fn, '/api/v2/users/me')).toBe(0)

  await userEvent.click(screen.getByRole('link', { name: 'Go to Wishbone' }))
  expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
  expect(window.location.pathname).toBe('/')

  act(() => window.history.back())
  expect(await heading("This link doesn't work")).toBeInTheDocument()
})

test('a link that fails after a good check shows LinkProblem as a history entry', async () => {
  let check: Reply = valid
  mockFetch({ [CHECK]: () => check, [ME]: noSession, [REDEEM]: badLink })
  open()
  await userEvent.click(await screen.findByRole('button', { name: 'Sign in' }))
  expect(await heading("This link doesn't work")).toBeInTheDocument()
  expect(document.activeElement).toBe(screen.getByRole('main'))

  // Back checks the link again, so it can't offer a dead button.
  check = badLink
  act(() => window.history.back())
  await waitFor(() => expect(window.history.state?.signInStep).toBeUndefined())
  expect(await heading("This link doesn't work")).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Sign in' })).not.toBeInTheDocument()
})

test('a rate-limited check shows "Too many tries", and Try again checks again', async () => {
  let check: Reply = { status: 429, body: { detail: 'Too many attempts.' } }
  mockFetch({ [CHECK]: () => check, [ME]: noSession })
  open()
  expect(await heading('Too many tries')).toBeInTheDocument()
  expect(screen.getByText('Wait a minute, then try again.')).toBeInTheDocument()

  check = valid
  await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
  expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  expect(document.activeElement).toBe(screen.getByRole('main'))
})

test('a rate-limited redemption shows "Too many tries", and Try again goes back to the page', async () => {
  mockFetch({ [CHECK]: valid, [ME]: noSession, [REDEEM]: { status: 429 } })
  open()
  await userEvent.click(await screen.findByRole('button', { name: 'Sign in' }))
  expect(await heading('Too many tries')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
  expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  expect(screen.queryByRole('heading', { name: 'Too many tries' })).not.toBeInTheDocument()
  expect(document.activeElement).toBe(screen.getByRole('main'))
})

test.each<[string, Reply]>([
  ['the network fails', 'offline'],
  ['the server fails', { status: 500 }],
])('a check is not a bad link when %s, and Try again recovers', async (_name, failure) => {
  let check: Reply = failure
  mockFetch({ [CHECK]: () => check, [ME]: noSession })
  open()
  expect(await heading("Can't reach Wishbone")).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Sign in' })).not.toBeInTheDocument()
  expect(screen.queryByText("This link doesn't work")).not.toBeInTheDocument()
  expect(screen.queryByRole('link', { name: 'Menu' })).not.toBeInTheDocument()

  check = valid
  await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
  expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
})

test('with no answer on who is signed in, the page shows no button that skips the warning', async () => {
  let me: Reply = 'offline'
  mockFetch({ [CHECK]: valid, [ME]: () => me })
  open()
  expect(await heading("Can't reach Wishbone")).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Sign in' })).not.toBeInTheDocument()

  me = { status: 200, body: john }
  await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
  expect(await heading('Signed in as John')).toBeInTheDocument()
})

test('a redemption that cannot reach the server keeps the page and says so', async () => {
  mockFetch({ [CHECK]: valid, [ME]: noSession, [REDEEM]: 'offline' })
  open()
  await userEvent.click(await screen.findByRole('button', { name: 'Sign in' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Reload and try again.')
  expect(screen.getByRole('button', { name: 'Sign in' })).toBeEnabled()
})
