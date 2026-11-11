import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { App } from './App'
import type { Session, User } from './api'

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
const grace: User = { ...ada, id: '8', name: 'Grace', last_name: null, is_admin: false }

const chrome =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36'
const here: Session = { id: '1', user_agent: chrome, last_used_at: '2026-11-08T12:00:00Z', created_at: '2026-10-01T12:00:00Z', is_current: true, can_delete: true }
const phone: Session = { id: '2', user_agent: 'Wishbone iOS/1.0', last_used_at: '2026-11-01T12:00:00Z', created_at: '2026-10-02T12:00:00Z', is_current: false, can_delete: true }
const envelope = (data: Session[]) => ({ status: 200, body: { data, next_cursor: null, total_count: data.length } })
const problem = (status: number, code: string) => ({ status, body: { status, code, detail: 'words' } })

type Answer = { status: number; body?: unknown }
function serve(routes: Record<string, Answer | (() => Answer)>) {
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const key = `${init?.method ?? 'GET'} ${String(input)}`
    const route = routes[key]
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

const heading = (name: string) => screen.findByRole('heading', { level: 1, name })
const ME = 'GET /api/v2/users/me'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

describe('the sign-in screen of the signed-in user', () => {
  const routes = () => ({
    [ME]: { status: 200, body: ada },
    'GET /api/v2/users/7': { status: 200, body: ada },
    'GET /api/v2/users/7/sessions': envelope([here, phone]),
    'GET /api/v2/users/7/passkeys': { status: 200, body: { data: [], next_cursor: null, total_count: 0 } },
  })

  test('lists the sessions, newest first as the server sorts them, and marks this device', async () => {
    serve(routes())
    open('/users/7/sign-in')

    expect(await heading('Your devices')).toBeInTheDocument()
    const rows = (await screen.findAllByRole('listitem')) as [HTMLElement, HTMLElement]
    expect(within(rows[0]).getByText('Chrome on macOS')).toBeInTheDocument()
    expect(within(rows[0]).getByText('This device, in use now')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Wishbone iOS')).toBeInTheDocument()
    expect(within(rows[1]).getByText(/Last used/)).toBeInTheDocument()
    expect(within(rows[0]).getByRole('button', { name: 'Revoke' })).toBeInTheDocument()
    expect(within(rows[1]).getByRole('button', { name: 'Revoke' })).toBeInTheDocument()
  })

  test('says so when no session is left', async () => {
    serve({ ...routes(), 'GET /api/v2/users/7/sessions': envelope([]) })
    open('/users/7/sign-in')

    expect(await screen.findByText('Not signed in on any device')).toBeInTheDocument()
    expect(screen.queryByRole('listitem')).not.toBeInTheDocument()
  })

  test('Revoke ends a session and takes it off the list', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...routes(), 'DELETE /api/v2/users/7/sessions/2': { status: 204 } })
    open('/users/7/sign-in')
    await heading('Your devices')

    const row = (await screen.findAllByRole('listitem'))[1]!
    await user.click(within(row).getByRole('button', { name: 'Revoke' }))

    await waitFor(() => expect(screen.getAllByRole('listitem')).toHaveLength(1))
    expect(screen.queryByText('Wishbone iOS')).not.toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/users/7/sessions/2', expect.objectContaining({ method: 'DELETE' }))
  })

  test('revoking this device signs the user out', async () => {
    const user = userEvent.setup()
    let out = false
    serve({
      ...routes(),
      [ME]: () => (out ? problem(401, 'unauthorized') : { status: 200, body: ada }),
      'GET /api/v2/setup': { status: 200, body: { is_needed: false } },
      'DELETE /api/v2/users/7/sessions/1': () => {
        out = true
        return { status: 204 }
      },
    })
    open('/users/7/sign-in')
    await heading('Your devices')

    await user.click(within((await screen.findAllByRole('listitem'))[0]!).getByRole('button', { name: 'Revoke' }))

    expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
  })

  test('a revoke that fails says so and keeps the session on the list', async () => {
    const user = userEvent.setup()
    serve({ ...routes(), 'DELETE /api/v2/users/7/sessions/2': { status: 500 } })
    open('/users/7/sign-in')
    await heading('Your devices')

    await user.click(within((await screen.findAllByRole('listitem'))[1]!).getByRole('button', { name: 'Revoke' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not revoke that session. Try again.')
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
  })

  test('Make a login link shows the QR code first, and the link once the person copies it', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...routes(),
      'POST /api/v2/users/7/login-links': {
        status: 201,
        body: { url: 'https://wish.example/login/tok123', expires_at: '2026-11-15T12:00:00Z' },
      },
    })
    open('/users/7/sign-in')
    await heading('Your devices')
    expect(screen.queryByRole('img')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Make a login link' }))

    const qr = await screen.findByRole('img', { name: 'QR code for the login link' })
    expect(qr.getAttribute('src')).toMatch(/^data:image\/svg\+xml/)
    expect(screen.queryByLabelText('Login link')).not.toBeInTheDocument()
    expect(screen.getByText(/^Works once\. Expires in/)).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/users/7/login-links', expect.objectContaining({ method: 'POST' }))

    Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined) }, configurable: true })
    await user.click(screen.getByRole('button', { name: 'Copy the link instead' }))
    expect(await screen.findByLabelText('Login link')).toHaveValue('https://wish.example/login/tok123')
    const link = screen.getByLabelText('Login link')
    expect(qr.compareDocumentPosition(link) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  test('a login link that could not be made says so', async () => {
    const user = userEvent.setup()
    serve({ ...routes(), 'POST /api/v2/users/7/login-links': { status: 500 } })
    open('/users/7/sign-in')
    await heading('Your devices')

    await user.click(screen.getByRole('button', { name: 'Make a login link' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not make a login link. Try again.')
  })
})

describe('the sign-in screen of another user', () => {
  test('is titled for them, and shows only what the can fields allow', async () => {
    serve({
      [ME]: { status: 200, body: ada },
      'GET /api/v2/users/8': { status: 200, body: { ...grace, can_make_login_link: false } },
      'GET /api/v2/users/8/sessions': envelope([{ ...phone, is_current: false }]),
      'GET /api/v2/users/8/passkeys': { status: 200, body: { data: [], next_cursor: null, total_count: 0 } },
    })
    open('/users/8/sign-in')

    expect(await heading('Sign Grace in')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Make a login link' })).not.toBeInTheDocument()
    expect(await screen.findByText('Wishbone iOS')).toBeInTheDocument()
  })

  test('has no sessions part when the caller cannot manage them', async () => {
    const fetch = serve({
      [ME]: { status: 200, body: ada },
      'GET /api/v2/users/8': { status: 200, body: { ...grace, can_manage_sessions: false } },
    })
    open('/users/8/sign-in')

    await heading('Sign Grace in')
    expect(screen.getByRole('button', { name: 'Make a login link' })).toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/8/sessions', expect.anything())
  })

  test('shows the missing-page message for a user the caller cannot see', async () => {
    serve({ [ME]: { status: 200, body: ada }, 'GET /api/v2/users/9': problem(404, 'not_found') })
    open('/users/9/sign-in')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('a session that ended in another tab', () => {
  test('sends the screen to the signed-out page', async () => {
    let out = false
    serve({
      [ME]: () => (out ? problem(401, 'unauthorized') : { status: 200, body: ada }),
      'GET /api/v2/setup': { status: 200, body: { is_needed: false } },
      'GET /api/v2/users/7': () => {
        out = true
        return problem(401, 'unauthorized')
      },
    })
    open('/users/7/sign-in')

    expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: "Can't reach Wishbone" })).not.toBeInTheDocument()
  })
})

test('Menu opens the sign-in screen of the signed-in user as Your devices', async () => {
  serve({ [ME]: { status: 200, body: ada } })
  open('/menu')

  const link = await screen.findByRole('link', { name: 'Your devices' })
  expect(link).toHaveAttribute('href', '/users/7/sign-in')
})
