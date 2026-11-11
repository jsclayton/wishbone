import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { User, UserSummary } from './api'
import { ada, family, grace, heading, ME, open, page, problem, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const signedInAsAda = { [ME]: { status: 200, body: ada } }
const when = '2026-11-08T14:00:00Z'

/** Grace, deactivated, as the admin sees her. */
const paused: User = { ...grace, deactivated_at: when, can_edit: false, can_make_login_link: false, can_manage_sessions: false, can_deactivate: false, can_reactivate: true }

/** Emma, a deactivated child, as a parent who isn't the admin sees her. */
const emma: User = {
  ...paused,
  id: '9',
  name: 'Emma',
  last_name: 'Lovelace',
  can_edit_parents: false,
  can_reactivate: false,
  parents: [{ id: '10', name: 'Mom', avatar: null }],
}
const mom: User = { ...grace, id: '10', name: 'Mom', last_name: 'Lovelace', can_edit_parents: false, can_deactivate: false, children: [{ id: '9', name: 'Emma', avatar: null } satisfies UserSummary] }

describe("a user's page", () => {
  test('shows Deactivate for an active user, which opens the confirmation screen and does nothing yet', async () => {
    const fetch = serve({ ...signedInAsAda, 'GET /api/v2/users/8': { status: 200, body: grace } })
    open('/users/8')

    const part = within(await screen.findByRole('region', { name: 'For Grace' }))
    expect(part.getByRole('link', { name: 'Deactivate Grace' })).toHaveAttribute('href', '/users/8/deactivate')
    expect(part.queryByRole('button', { name: 'Reactivate Grace' })).not.toBeInTheDocument()
    expect(screen.queryByText('Deactivated')).not.toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/8/deactivate', expect.anything())
  })

  test('shows Deactivated beside the name, and Reactivate in place of Deactivate, for a deactivated user', async () => {
    serve({ ...signedInAsAda, 'GET /api/v2/users/8': { status: 200, body: paused } })
    open('/users/8')

    const part = within(await screen.findByRole('region', { name: 'For Grace' }))
    expect(screen.getByText('Deactivated')).toBeInTheDocument()
    expect(part.getByRole('button', { name: 'Reactivate Grace' })).toBeInTheDocument()
    expect(part.queryByRole('link', { name: 'Deactivate Grace' })).not.toBeInTheDocument()
    expect(part.queryByRole('link', { name: /profile/ })).not.toBeInTheDocument()
    expect(part.getByRole('heading', { level: 3, name: 'Parents' }), 'the admin still changes the parents').toBeInTheDocument()
  })

  test('Reactivate asks for no confirmation and brings the page back to active', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...signedInAsAda,
      'GET /api/v2/users/8': { status: 200, body: paused },
      'POST /api/v2/users/8/reactivate': { status: 200, body: grace },
    })
    open('/users/8')

    await user.click(await screen.findByRole('button', { name: 'Reactivate Grace' }))

    expect(await screen.findByRole('link', { name: 'Deactivate Grace' })).toBeInTheDocument()
    expect(screen.queryByText('Deactivated')).not.toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/users/8/reactivate', expect.objectContaining({ method: 'POST' }))
    expect(window.location.pathname).toBe('/users/8')
  })

  test('a Reactivate that fails says so and keeps the page', async () => {
    const user = userEvent.setup()
    serve({
      ...signedInAsAda,
      'GET /api/v2/users/8': { status: 200, body: paused },
      'GET /api/v2/lists?recipient_id=8&is_primary=false': page([]),
      'POST /api/v2/users/8/reactivate': problem(500, 'internal_error'),
    })
    open('/users/8')

    await user.click(await screen.findByRole('button', { name: 'Reactivate Grace' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not reactivate Grace. Try again.')
    expect(screen.getByText('Deactivated')).toBeInTheDocument()
  })

  test("tells a parent who isn't the admin that only the admin reactivates, and shows no For part", async () => {
    serve({
      [ME]: { status: 200, body: mom },
      'GET /api/v2/users/9': { status: 200, body: emma },
    })
    open('/users/9')

    expect(await heading('Emma Lovelace Deactivated'), 'Deactivated is inline after the name').toBeInTheDocument()
    expect(screen.getByText('Deactivated')).toBeInTheDocument()
    expect(screen.getByText("Only the admin can reactivate Emma. Until then, you can't change anything for Emma.")).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'For Emma' })).not.toBeInTheDocument()
  })

  test('has neither the line nor the label for an active child', async () => {
    serve({
      [ME]: { status: 200, body: mom },
      'GET /api/v2/users/9': { status: 200, body: { ...emma, deactivated_at: null, can_edit: true, can_make_login_link: true, can_manage_sessions: true } },
    })
    open('/users/9')

    await heading('Emma Lovelace')
    expect(screen.queryByText('Deactivated')).not.toBeInTheDocument()
    expect(screen.queryByText(/Only the admin can reactivate/)).not.toBeInTheDocument()
  })

  test('has neither for a user whose deactivated_at the viewer does not get', async () => {
    serve({
      [ME]: { status: 200, body: mom },
      'GET /api/v2/users/9': { status: 200, body: { ...emma, deactivated_at: undefined } },
    })
    open('/users/9')

    await heading('Emma Lovelace')
    expect(screen.queryByText('Deactivated')).not.toBeInTheDocument()
    expect(screen.queryByText(/Only the admin can reactivate/)).not.toBeInTheDocument()
  })
})

describe('the Deactivate confirmation screen', () => {
  const routes = () => ({ ...signedInAsAda, 'GET /api/v2/users/8': { status: 200, body: grace } })

  test('says what happens, and leaves the user alone until the admin confirms', async () => {
    const fetch = serve(routes())
    open('/users/8/deactivate')

    expect(await heading('Deactivate Grace?')).toBeInTheDocument()
    expect(screen.getByText(/Grace will be signed out everywhere/)).toBeInTheDocument()
    expect(screen.getByText(/You can reactivate Grace later/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Keep Grace active' })).toHaveAttribute('href', '/users/8')
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/8/deactivate', expect.anything())
  })

  test('deactivates on the confirm button, and opens the user page, where she shows as deactivated', async () => {
    const user = userEvent.setup()
    let deactivated = false
    const fetch = serve({
      ...signedInAsAda,
      'GET /api/v2/users/8': () => ({ status: 200, body: deactivated ? paused : grace }),
      'POST /api/v2/users/8/deactivate': () => {
        deactivated = true
        return { status: 200, body: paused }
      },
    })
    open('/users/8/deactivate')

    await user.click(await screen.findByRole('button', { name: 'Deactivate Grace' }))

    await waitFor(() => expect(window.location.pathname).toBe('/users/8'))
    expect(await screen.findByText('Deactivated')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/users/8/deactivate', expect.objectContaining({ method: 'POST' }))
  })

  test('says so when the deactivation fails, and stays', async () => {
    const user = userEvent.setup()
    serve({ ...routes(), 'POST /api/v2/users/8/deactivate': problem(403, 'admin_cannot_be_deactivated') })
    open('/users/8/deactivate')

    await user.click(await screen.findByRole('button', { name: 'Deactivate Grace' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(window.location.pathname).toBe('/users/8/deactivate')
  })

  test('shows the missing-page message to anyone who has no can_deactivate', async () => {
    serve({ ...signedInAsAda, 'GET /api/v2/users/8': { status: 200, body: { ...grace, can_deactivate: false } } })
    open('/users/8/deactivate')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('the Users screen', () => {
  test('shows Deactivated beside a deactivated user, and keeps them in their place', async () => {
    serve({ ...signedInAsAda, 'GET /api/v2/users': page([ada, paused, { ...grace, id: '9', name: 'Zed', last_name: null }]) })
    open('/users')

    const items = await screen.findAllByRole('listitem')
    expect(items.map((item) => item.textContent)).toEqual(['Ada Lovelace', 'Grace Hopper Deactivated', 'Zed'])
    expect(within(items[1]!).getByText('Deactivated')).toBeInTheDocument()
  })
})

describe('People on Home', () => {
  test('shows Deactivated beside a child whose deactivated_at the user gets', async () => {
    const momWithChild: User = { ...mom, can_edit_parents: false }
    serve({
      [ME]: { status: 200, body: momWithChild },
      'GET /api/v2/groups?includes_me=true': page([{ ...family, can_edit: false }]),
      'GET /api/v2/users?group_id=5': page([momWithChild, { ...emma, id: '11', name: 'Hedy', last_name: null, deactivated_at: null }]),
      'GET /api/v2/users': page([momWithChild, emma]),
    })
    open('/')

    const people = within(await screen.findByRole('region', { name: 'People' }))
    const children = within(await people.findByRole('region', { name: 'Your children' }))
    expect(children.getByRole('link', { name: 'Emma Lovelace Deactivated' })).toHaveAttribute('href', '/users/9')
    const group = within(people.getByRole('region', { name: 'My family' }))
    expect(group.getByRole('link', { name: 'Hedy' })).toBeInTheDocument()
    expect(group.queryByText('Deactivated')).not.toBeInTheDocument()
  })
})

describe('the sign-in screen of a deactivated user', () => {
  test("shows the sessions and the passkeys to the parent who reads them, with no way to change them", async () => {
    const passkey = { id: '3', name: 'Phone', created_at: when, last_used_at: null, can_edit: false, can_delete: false, version: 'v' }
    const fetch = serve({
      [ME]: { status: 200, body: mom },
      'GET /api/v2/users/9': { status: 200, body: emma },
      'GET /api/v2/users/9/sessions': page([]),
      'GET /api/v2/users/9/passkeys': page([passkey]),
    })
    open('/users/9/sign-in')

    expect(await heading('Sign Emma in')).toBeInTheDocument()
    expect(await screen.findByText('Not signed in on any device')).toBeInTheDocument()
    expect(await screen.findByText('Phone')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Make a login link' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Delete/ })).not.toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/users/9/passkeys', expect.anything())
  })
})
