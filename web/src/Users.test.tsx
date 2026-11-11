import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { User } from './api'
import { ada, grace, heading, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const signedInAsAda = { [ME]: { status: 200, body: ada } }
const graceSelf: User = { ...grace, can_confirm_profile: false, profile_confirmed_at: '2026-11-08T13:00:00Z' }

describe('Menu', () => {
  test('has a For the admin part with Users for the admin', async () => {
    serve(signedInAsAda)
    open('/menu')

    const part = await screen.findByRole('heading', { name: 'For the admin' })
    expect(part).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('href', '/users')
  })

  test('has no For the admin part for anyone else', async () => {
    serve({ [ME]: { status: 200, body: graceSelf } })
    open('/menu')

    await screen.findByRole('link', { name: 'Your devices' })
    expect(screen.queryByRole('heading', { name: 'For the admin' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Users' })).not.toBeInTheDocument()
  })

  test('Your profile opens the profile screen with the ID of the signed-in user', async () => {
    serve(signedInAsAda)
    open('/menu')

    expect(await screen.findByRole('link', { name: 'Your profile' })).toHaveAttribute('href', '/users/7/profile')
  })
})

describe('the Users screen', () => {
  test('lists every user by name, following the cursor to the last page', async () => {
    const fetch = serve({
      ...signedInAsAda,
      'GET /api/v2/users': page([ada, grace], 'abc'),
      'GET /api/v2/users?cursor=abc': page([{ ...grace, id: '9', name: 'Zed', last_name: null }]),
    })
    open('/users')

    expect(await heading('Users')).toBeInTheDocument()
    const items = await screen.findAllByRole('listitem')
    expect(items.map((item) => item.textContent)).toEqual(['Ada Lovelace', 'Grace Hopper', 'Zed'])
    expect(within(items[1]!).getByRole('link', { name: 'Grace Hopper' })).toHaveAttribute('href', '/users/8')
    expect(screen.getByRole('link', { name: 'New user' })).toHaveAttribute('href', '/users/new')
    expect(fetch).toHaveBeenCalledWith('/api/v2/users?cursor=abc', expect.anything())
  })

  test('shows the missing-page message to anyone but the admin, and asks for no users', async () => {
    const fetch = serve({ [ME]: { status: 200, body: graceSelf } })
    open('/users')

    expect(await heading("We can't find that page")).toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users', expect.anything())
  })
})

describe('the New user screen', () => {
  async function fillIn(user: ReturnType<typeof userEvent.setup>, { year = '' } = {}) {
    await user.type(screen.getByLabelText('First name'), ' Grace ')
    await user.type(screen.getByLabelText('Last name (optional)'), 'Hopper')
    await user.selectOptions(screen.getByLabelText('Month'), 'December')
    await user.type(screen.getByLabelText('Day'), '9')
    if (year) await user.type(screen.getByLabelText('Year'), year)
  }

  test('asks for a name, a birthday, and an optional last name, then opens the new user', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...signedInAsAda,
      'POST /api/v2/users': { status: 201, body: { ...grace, id: '8' } },
      'GET /api/v2/users/8': { status: 200, body: grace },
    })
    open('/users/new')
    await heading('New user')

    await fillIn(user, { year: '1906' })
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await heading('Grace Hopper')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/users')).toEqual({
      name: 'Grace',
      last_name: 'Hopper',
      birthday: { month: 12, day: 9, year: 1906 },
    })
    expect(window.location.pathname).toBe('/users/8')
  })

  test('sends no last name and no year when they are left out', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...signedInAsAda,
      'POST /api/v2/users': { status: 201, body: { ...grace, id: '8' } },
      'GET /api/v2/users/8': { status: 200, body: grace },
    })
    open('/users/new')
    await heading('New user')

    await user.type(screen.getByLabelText('First name'), 'Grace')
    await user.selectOptions(screen.getByLabelText('Month'), 'December')
    await user.type(screen.getByLabelText('Day'), '9')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await heading('Grace Hopper')
    expect(sentBody(fetch, 'POST /api/v2/users')).toEqual({ name: 'Grace', birthday: { month: 12, day: 9, year: null } })
  })

  test('does not ask the server until the name and the birthday month and day are there', async () => {
    const user = userEvent.setup()
    const fetch = serve(signedInAsAda)
    open('/users/new')
    await heading('New user')

    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Enter a first name and a birthday month and day.')
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users', expect.objectContaining({ method: 'POST' }))
  })

  test('shows the words of the server when it refuses the input, and keeps the form', async () => {
    const user = userEvent.setup()
    serve({
      ...signedInAsAda,
      'POST /api/v2/users': problem(422, 'invalid_input', 'The year must be 1900 to 2026.'),
    })
    open('/users/new')
    await heading('New user')

    await fillIn(user, { year: '1800' })
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('The year must be 1900 to 2026.')
    expect(screen.getByLabelText('First name')).toHaveValue(' Grace ')
  })

  test('shows the missing-page message to anyone but the admin', async () => {
    serve({ [ME]: { status: 200, body: graceSelf } })
    open('/users/new')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe("a user's page", () => {
  test('has the For part, with a button for each can field that is true', async () => {
    serve({ ...signedInAsAda, 'GET /api/v2/users/8': { status: 200, body: graceSelf } })
    open('/users/8')

    expect(await heading('Grace Hopper')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 2, name: 'For Grace' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: "Edit Grace's profile" })).toHaveAttribute('href', '/users/8/profile')
    expect(screen.getByRole('link', { name: 'Sign Grace in' })).toHaveAttribute('href', '/users/8/sign-in')
  })

  test('leaves out a button whose can field is false', async () => {
    serve({
      ...signedInAsAda,
      'GET /api/v2/users/8': { status: 200, body: { ...graceSelf, can_edit: false } },
    })
    open('/users/8')

    await heading('Grace Hopper')
    expect(screen.queryByRole('link', { name: "Edit Grace's profile" })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Sign Grace in' })).toBeInTheDocument()
  })

  test('Sign in shows when the caller can make a login link or manage sessions, either one', async () => {
    serve({
      ...signedInAsAda,
      'GET /api/v2/users/8': { status: 200, body: { ...graceSelf, can_make_login_link: false } },
    })
    open('/users/8')

    expect(await screen.findByRole('link', { name: 'Sign Grace in' })).toBeInTheDocument()
  })

  test('has no For part when no button would show', async () => {
    serve({
      ...signedInAsAda,
      'GET /api/v2/users/8': {
        status: 200,
        body: { ...graceSelf, can_edit: false, can_make_login_link: false, can_manage_sessions: false, can_edit_parents: false, can_deactivate: false },
      },
    })
    open('/users/8')

    await heading('Grace Hopper')
    expect(screen.queryByRole('heading', { level: 2 })).not.toBeInTheDocument()
  })

  test('shows the missing-page message for a user the caller cannot see', async () => {
    serve({ ...signedInAsAda, 'GET /api/v2/users/9': problem(404, 'not_found') })
    open('/users/9')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('the profile screen', () => {
  const routes = (body: User = graceSelf) => ({
    ...signedInAsAda,
    'GET /api/v2/users/8': { status: 200, body },
    'GET /api/v2/occasions?user_id=8': page([]),
  })

  test("has the profile fields, filled in, and no timezone field", async () => {
    serve(routes({ ...graceSelf, good_to_know: 'Likes tea', timezone: 'Europe/Paris' }))
    open('/users/8/profile')

    expect(await heading("Grace's profile")).toBeInTheDocument()
    expect(screen.getByLabelText('First name')).toHaveValue('Grace')
    expect(screen.getByLabelText('Last name (optional)')).toHaveValue('Hopper')
    expect(screen.getByLabelText('Month')).toHaveValue('12')
    expect(screen.getByLabelText('Day')).toHaveValue(9)
    expect(screen.getByLabelText('Year')).toHaveValue(1906)
    expect(screen.getByLabelText('Good to know')).toHaveValue('Likes tea')
    expect(screen.queryByLabelText(/time ?zone/i)).not.toBeInTheDocument()
    expect(screen.queryByDisplayValue('Europe/Paris')).not.toBeInTheDocument()
  })

  test('is titled Your profile for the user themselves', async () => {
    serve({ ...signedInAsAda, 'GET /api/v2/users/7': { status: 200, body: ada }, 'GET /api/v2/occasions?user_id=7': page([]) })
    open('/users/7/profile')

    expect(await heading('Your profile')).toBeInTheDocument()
  })

  test('Save sends only the fields that changed, as a merge patch, and says Saved', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...routes(),
      'PATCH /api/v2/users/8': (init) => ({ status: 200, body: { ...graceSelf, ...JSON.parse(String(init?.body)) } }),
    })
    open('/users/8/profile')
    await heading("Grace's profile")

    await user.clear(screen.getByLabelText('First name'))
    await user.type(screen.getByLabelText('First name'), 'Gracie')
    await user.type(screen.getByLabelText('Good to know'), 'Likes tea')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Saved.')).toBeInTheDocument()
    expect(sentBody(fetch, 'PATCH /api/v2/users/8')).toEqual({ name: 'Gracie', good_to_know: 'Likes tea' })
    const call = fetch.mock.calls.find(([, init]) => init?.method === 'PATCH')!
    expect(new Headers(call[1]?.headers).get('Content-Type')).toBe('application/merge-patch+json')
  })

  test('clearing the last name or the note sends null', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...routes({ ...graceSelf, good_to_know: 'Likes tea' }),
      'PATCH /api/v2/users/8': { status: 200, body: { ...graceSelf, last_name: null } },
    })
    open('/users/8/profile')
    await heading("Grace's profile")

    await user.clear(screen.getByLabelText('Last name (optional)'))
    await user.clear(screen.getByLabelText('Good to know'))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await screen.findByText('Saved.')
    expect(sentBody(fetch, 'PATCH /api/v2/users/8')).toEqual({ last_name: null, good_to_know: null })
  })

  test('a changed birthday is sent whole', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...routes(),
      'PATCH /api/v2/users/8': { status: 200, body: graceSelf },
    })
    open('/users/8/profile')
    await heading("Grace's profile")

    await user.clear(screen.getByLabelText('Year'))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await screen.findByText('Saved.')
    expect(sentBody(fetch, 'PATCH /api/v2/users/8')).toEqual({ birthday: { month: 12, day: 9, year: null } })
  })

  test('asks for nothing when nothing changed', async () => {
    const user = userEvent.setup()
    const fetch = serve(routes())
    open('/users/8/profile')
    await heading("Grace's profile")

    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Saved.')).toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/8', expect.objectContaining({ method: 'PATCH' }))
  })

  test('needs a first name and a birthday month and day', async () => {
    const user = userEvent.setup()
    const fetch = serve(routes())
    open('/users/8/profile')
    await heading("Grace's profile")

    await user.clear(screen.getByLabelText('First name'))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter a first name.')

    await user.type(screen.getByLabelText('First name'), 'Grace')
    await user.clear(screen.getByLabelText('Day'))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Choose a birthday month and day.'))
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/8', expect.objectContaining({ method: 'PATCH' }))
  })

  test("shows the server's words when it refuses a change", async () => {
    const user = userEvent.setup()
    serve({
      ...routes(),
      'PATCH /api/v2/users/8': problem(422, 'invalid_input', 'That date is not in the calendar.'),
    })
    open('/users/8/profile')
    await heading("Grace's profile")

    await user.clear(screen.getByLabelText('Day'))
    await user.type(screen.getByLabelText('Day'), '31')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('That date is not in the calendar.')
    expect(screen.queryByText('Saved.')).not.toBeInTheDocument()
  })

  test("an edit of another user's profile does not confirm it", async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...routes({ ...grace, profile_confirmed_at: null }),
      'PATCH /api/v2/users/8': { status: 200, body: grace },
    })
    open('/users/8/profile')
    await heading("Grace's profile")

    await user.type(screen.getByLabelText('Good to know'), 'x')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await screen.findByText('Saved.')
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/8/confirm-profile', expect.anything())
  })

  test('a user who saves their own profile gets their new name in Menu', async () => {
    const user = userEvent.setup()
    let name = 'Ada'
    serve({
      [ME]: () => ({ status: 200, body: { ...ada, name } }),
      'GET /api/v2/users/7': { status: 200, body: ada },
      'GET /api/v2/occasions?user_id=7': page([]),
      'PATCH /api/v2/users/7': () => {
        name = 'Augusta'
        return { status: 200, body: { ...ada, name } }
      },
    })
    open('/users/7/profile')
    await heading('Your profile')

    await user.clear(screen.getByLabelText('First name'))
    await user.type(screen.getByLabelText('First name'), 'Augusta')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await screen.findByText('Saved.')
    await user.click(screen.getByRole('link', { name: 'Menu' }))

    expect(await screen.findByText('Augusta Lovelace')).toBeInTheDocument()
  })

  test('shows the missing-page message when the caller cannot edit the user', async () => {
    serve(routes({ ...graceSelf, can_edit: false }))
    open('/users/8/profile')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('the finish-profile step', () => {
  const newbie = { ...grace, id: '9', name: 'Zed', last_name: null, can_confirm_profile: true, is_admin: false }

  test('shows at any path until the profile is confirmed, filled in with what the admin entered', async () => {
    serve({ [ME]: { status: 200, body: newbie } })
    open('/menu')

    expect(await heading('Finish your profile')).toBeInTheDocument()
    expect(screen.getByLabelText('First name')).toHaveValue('Zed')
    expect(screen.getByLabelText('Month')).toHaveValue('12')
    expect(screen.queryByRole('link', { name: 'Menu' })).not.toBeInTheDocument()
  })

  test('Continue saves the changes, the photo step confirms the profile, and the screen the person came for opens', async () => {
    const user = userEvent.setup()
    let confirmed = false
    const fetch = serve({
      [ME]: () => ({ status: 200, body: confirmed ? { ...newbie, can_confirm_profile: false, profile_confirmed_at: '2026-11-09T09:00:00Z' } : newbie }),
      'PATCH /api/v2/users/9': { status: 200, body: newbie },
      'POST /api/v2/users/9/confirm-profile': () => {
        confirmed = true
        return { status: 200, body: { ...newbie, can_confirm_profile: false } }
      },
    })
    open('/menu')
    await heading('Finish your profile')

    await user.type(screen.getByLabelText('Last name (optional)'), 'Zeta')
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await user.click(await screen.findByRole('button', { name: 'Not now' }))

    expect(await heading('Menu')).toBeInTheDocument()
    expect(sentBody(fetch, 'PATCH /api/v2/users/9')).toEqual({ last_name: 'Zeta' })
    expect(fetch).toHaveBeenCalledWith('/api/v2/users/9/confirm-profile', expect.objectContaining({ method: 'POST' }))
  })

  test('confirms without a patch when nothing changed', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      [ME]: { status: 200, body: newbie },
      'POST /api/v2/users/9/confirm-profile': { status: 200, body: { ...newbie, can_confirm_profile: false } },
    })
    open('/')
    await heading('Finish your profile')

    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await user.click(await screen.findByRole('button', { name: 'Not now' }))

    await waitFor(() => expect(fetch).toHaveBeenCalledWith('/api/v2/users/9/confirm-profile', expect.anything()))
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/9', expect.objectContaining({ method: 'PATCH' }))
  })

  test('needs a first name and a birthday, and stays when the server refuses', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      [ME]: { status: 200, body: newbie },
      'PATCH /api/v2/users/9': problem(422, 'invalid_input', 'The name must be at most 100 characters.'),
    })
    open('/')
    await heading('Finish your profile')

    await user.clear(screen.getByLabelText('First name'))
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter your first name, birthday month, and birthday day.')

    await user.type(screen.getByLabelText('First name'), 'Zedd')
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('The name must be at most 100 characters.'))
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/9/confirm-profile', expect.anything())
  })

  test('has a way to log out, so nobody is stuck', async () => {
    const user = userEvent.setup()
    serve({
      [ME]: { status: 200, body: newbie },
      'DELETE /api/v2/users/me/sessions/current': { status: 204 },
      'GET /api/v2/setup': { status: 200, body: { is_needed: false } },
    })
    open('/')
    await heading('Finish your profile')

    await user.click(screen.getByRole('button', { name: 'Log out' }))

    expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
  })
})
