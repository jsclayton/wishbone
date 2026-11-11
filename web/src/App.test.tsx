import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { App } from './App'
import type { User } from './api'
import { browserZone } from './test/browserZone'

const ada: User = {
  id: '7',
  name: 'Ada',
  last_name: 'Lovelace',
  avatar: null,
  birthday: { month: 12, day: 10, year: null },
  is_admin: true,
  good_to_know: null,
  timezone: null,
  profile_confirmed_at: '2026-11-08T12:00:00Z',
  created_at: '2026-11-08T12:00:00Z',
  can_edit: true,
  can_make_login_link: true,
  can_manage_sessions: true,
  can_confirm_profile: false,
  can_edit_parents: false,
  can_deactivate: false,
  can_reactivate: false,
  can_add_occasion: true,
  parents: [],
  children: [],
}

const problem = (status: number, code: string) => ({ status, body: { status, code, detail: 'words' } })

/** A fake server: each route answers with a status and a JSON body. */
type Answer = { status: number; body?: unknown }
const noOccasions: Answer = { status: 200, body: { data: [], next_cursor: null, total_count: 0 } }
function serve(routes: Record<string, Answer | (() => Answer)>) {
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const key = `${init?.method ?? 'GET'} ${String(input)}`
    // Home's Coming up asks for the next 60 days, and these tests are not about it.
    const route = routes[key] ?? (key.startsWith('GET /api/v2/occasions?next_date_before=') ? noOccasions : undefined)
    if (!route) throw new Error(`no fake for ${key}`)
    const { status, body } = typeof route === 'function' ? route() : route
    return new Response(body === undefined ? null : JSON.stringify(body), { status })
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

function open(path: string) {
  window.history.replaceState(null, '', path)
  return render(<App />)
}

const signedOut = {
  'GET /api/v2/users/me': problem(401, 'unauthorized'),
  'GET /api/v2/setup': { status: 200, body: { is_needed: false } },
}
// Home shows People, which asks for the user's own groups.
const peopleOfNobody = { 'GET /api/v2/groups?includes_me=true': { status: 200, body: { data: [], next_cursor: null, total_count: 0 } } }
// Home counts the invitations for the user and their children.
const noInvitations = { 'GET /api/v2/invitations?user_id=7&limit=1': { status: 200, body: { data: [], next_cursor: null, total_count: 0 } } }
// Menu shows the version the server reports.
const health = { 'GET /api/v2/health': { status: 200, body: { status: 'ok', version: '2.1.0+abc1234', commit: 'abc1234' } } }
const signedIn = { 'GET /api/v2/users/me': { status: 200, body: ada }, ...peopleOfNobody, ...noInvitations, ...health }

const heading = (name: string) => screen.findByRole('heading', { level: 1, name })

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

describe('a browser with no session', () => {
  test('sees the signed-out page, which names no one and has no Menu', async () => {
    serve(signedOut)
    open('/')
    expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Wishbone' })).toHaveAttribute('href', '/')
    expect(screen.queryByRole('link', { name: 'Menu' })).not.toBeInTheDocument()
  })

  test('sees the setup screen while the instance has no users', async () => {
    serve({ ...signedOut, 'GET /api/v2/setup': { status: 200, body: { is_needed: true } } })
    open('/')
    expect(await heading('Set up Wishbone')).toBeInTheDocument()
  })

  test('sees the setup screen at any path', async () => {
    serve({ ...signedOut, 'GET /api/v2/setup': { status: 200, body: { is_needed: true } } })
    open('/lists/3')
    expect(await heading('Set up Wishbone')).toBeInTheDocument()
  })

  test('does not ask whether setup is needed when there is a session', async () => {
    const fetch = serve(signedIn)
    open('/')
    await heading('Home')
    // People asks for the groups from an effect, which can still be pending when Home shows.
    await waitFor(() => expect(fetch.mock.calls.map(([url]) => String(url))).toEqual(['/api/v2/users/me', '/api/v2/groups?includes_me=true', '/api/v2/gifts?giver_id=me&state=open&needs_attention=true', '/api/v2/invitations?user_id=7&limit=1', expect.stringMatching(/^\/api\/v2\/occasions\?next_date_before=/)]))
  })

  test.each(['/list', '/people/3', '/profile', '/devices', '/nothing/here'])('%s opens the signed-out page', async (path) => {
    serve(signedOut)
    open(path)
    expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
  })
})

describe('setup', () => {
  test('finishing setup opens Home, signed in as the new admin', async () => {
    const user = userEvent.setup()
    let created = false
    serve({
      'GET /api/v2/users/me': () => (created ? { status: 200, body: ada } : problem(401, 'unauthorized')),
      'GET /api/v2/setup': { status: 200, body: { is_needed: true } },
      'POST /api/v2/setup': () => {
        created = true
        return { status: 201, body: ada }
      },
    })
    open('/')
    await heading('Set up Wishbone')

    await user.type(screen.getByLabelText('Setup code'), 'ABCD-EFGH-JKMN')
    await user.type(screen.getByLabelText('First name'), 'Ada')
    await user.selectOptions(screen.getByLabelText('Month'), 'December')
    await user.type(screen.getByLabelText('Day'), '10')
    await user.click(screen.getByRole('button', { name: 'Set up' }))

    expect(await heading('Home')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Menu' })).toBeInTheDocument()
  })
})

describe('a signed-in user', () => {
  test('sees Home, with the top row and Menu', async () => {
    serve(signedIn)
    open('/')
    expect(await heading('Home')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Menu' })).toHaveAttribute('href', '/menu')
    expect(screen.getByText(/Ada/)).toBeInTheDocument()
  })

  test.each(['/list', '/people/3', '/profile', '/devices', '/nothing/here'])('%s opens Home', async (path) => {
    serve(signedIn)
    open(path)
    expect(await heading('Home')).toBeInTheDocument()
  })

  test('Menu has Home and Log out', async () => {
    serve(signedIn)
    open('/menu')
    expect(await heading('Menu')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Home' })).toHaveAttribute('href', '/')
    expect(screen.getByRole('button', { name: 'Log out' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Source code' })).toBeInTheDocument()
    expect(await screen.findByText('Version 2.1.0+abc1234')).toBeInTheDocument()
  })

  test('a tap on Menu moves to it with no page load, and Home is a tap away', async () => {
    const user = userEvent.setup()
    const fetch = serve(signedIn)
    open('/')
    await heading('Home')

    await user.click(screen.getByRole('link', { name: 'Menu' }))
    expect(await heading('Menu')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/menu')
    expect(screen.getByRole('main')).toHaveFocus()
    expect(fetch).toHaveBeenCalledTimes(6) // Menu asks only for the version

    await user.click(screen.getByRole('link', { name: 'Home' }))
    expect(await heading('Home')).toBeInTheDocument()
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(10)) // only Home's invitation count, People, Not bought yet and Coming up ask again
  })

  test('Back returns to the earlier screen', async () => {
    const user = userEvent.setup()
    serve(signedIn)
    open('/')
    await heading('Home')
    await user.click(screen.getByRole('link', { name: 'Menu' }))
    await heading('Menu')

    window.history.replaceState(null, '', '/')
    window.dispatchEvent(new PopStateEvent('popstate'))

    expect(await heading('Home')).toBeInTheDocument()
  })

  test('Log out signs out, and opens the signed-out page at Home', async () => {
    const user = userEvent.setup()
    let out = false
    const fetch = serve({
      'GET /api/v2/users/me': () => (out ? problem(401, 'unauthorized') : { status: 200, body: ada }),
      'GET /api/v2/setup': { status: 200, body: { is_needed: false } },
      'DELETE /api/v2/users/me/sessions/current': () => {
        out = true
        return { status: 204 }
      },
    })
    open('/menu')
    await heading('Menu')

    await user.click(screen.getByRole('button', { name: 'Log out' }))

    expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/')
    expect(fetch.mock.calls.some(([url, init]) => String(url).endsWith('/sessions/current') && init?.method === 'DELETE')).toBe(true)
  })

  test('a failed Log out says so and keeps the user signed in', async () => {
    const user = userEvent.setup()
    serve({ ...signedIn, 'DELETE /api/v2/users/me/sessions/current': problem(500, 'internal_error') })
    open('/menu')
    await heading('Menu')

    await user.click(screen.getByRole('button', { name: 'Log out' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong')
    expect(screen.getByRole('heading', { level: 1, name: 'Menu' })).toBeInTheDocument()
  })
})

describe('a server that does not answer', () => {
  test('says Wishbone cannot be reached, and Try again asks again', async () => {
    const user = userEvent.setup()
    let up = false
    serve({ 'GET /api/v2/users/me': () => (up ? { status: 200, body: ada } : { status: 503 }) })
    open('/')
    expect(await heading("Can't reach Wishbone")).toBeInTheDocument()

    up = true
    await user.click(screen.getByRole('button', { name: 'Try again' }))

    expect(await heading('Home')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText("Can't reach Wishbone")).not.toBeInTheDocument())
  })
})

describe("the browser's timezone", () => {
  const patches = (fetch: ReturnType<typeof serve>) => fetch.mock.calls.filter(([url, init]) => String(url) === '/api/v2/users/me' && init?.method === 'PATCH')
  const meIn = (timezone: string | null) => ({ 'GET /api/v2/users/me': { status: 200, body: { ...ada, timezone } } })

  beforeEach(() => browserZone('Europe/Paris'))

  test("is reported to PATCH /users/me when it is not the user's, once, and the app shows Home", async () => {
    const fetch = serve({ ...signedIn, ...meIn(null), 'PATCH /api/v2/users/me': { status: 200, body: ada } })
    open('/')
    expect(await heading('Home')).toBeInTheDocument()

    await waitFor(() => expect(patches(fetch)).toHaveLength(1))
    const [, init] = patches(fetch)[0]!
    expect(init?.headers).toEqual({ 'Content-Type': 'application/merge-patch+json' })
    expect(JSON.parse(String(init?.body))).toEqual({ timezone: 'Europe/Paris' })
  })

  test('is reported when it differs from a timezone the user has', async () => {
    const fetch = serve({ ...signedIn, ...meIn('America/Denver'), 'PATCH /api/v2/users/me': { status: 200, body: ada } })
    open('/')
    await heading('Home')
    await waitFor(() => expect(patches(fetch)).toHaveLength(1))
  })

  test("is not reported when it is the user's", async () => {
    const fetch = serve({ ...signedIn, ...meIn('Europe/Paris') })
    open('/')
    await heading('Home')
    expect(patches(fetch)).toHaveLength(0)
  })

  test('is not reported when the browser does not know its zone', async () => {
    browserZone(undefined)
    const fetch = serve({ ...signedIn, ...meIn(null) })
    open('/')
    await heading('Home')
    expect(patches(fetch)).toHaveLength(0)
  })

  test('is not reported with no session', async () => {
    const fetch = serve(signedOut)
    open('/')
    await heading('Welcome to Wishbone')
    expect(patches(fetch)).toHaveLength(0)
  })

  test.each([
    ['a server error', () => problem(500, 'internal')],
    ['a refusal', () => problem(422, 'invalid_input')],
  ])('shows nothing and does not block the app after %s', async (_name, answer) => {
    const fetch = serve({ ...signedIn, ...meIn(null), 'PATCH /api/v2/users/me': answer })
    open('/')
    expect(await heading('Home')).toBeInTheDocument()
    await waitFor(() => expect(patches(fetch)).toHaveLength(1))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  test('shows nothing and does not block the app when the call fails outright', async () => {
    // serve() has no fake for the PATCH, so fetch rejects.
    const fetch = serve({ ...signedIn, ...meIn(null) })
    open('/')
    expect(await heading('Home')).toBeInTheDocument()
    await waitFor(() => expect(patches(fetch)).toHaveLength(1))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
