import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { User, UserSummary } from './api'
import { ada, family, grace, heading, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const summary = (u: User): UserSummary => ({ id: u.id, name: u.name, avatar: null })
const mom: User = { ...grace, id: '10', name: 'Mom', last_name: 'Lovelace' }
const dad: User = { ...grace, id: '11', name: 'Dad', last_name: 'Lovelace' }
const emma: User = { ...grace, id: '9', name: 'Emma', last_name: 'Lovelace' }
const hedy: User = { ...grace, id: '12', name: 'Hedy', last_name: 'Lamarr' }

/** Grace, signed in as herself, with a child and two parents. */
const graceSelf: User = {
  ...grace,
  can_edit_parents: false,
  can_deactivate: false,
  can_reactivate: false,
  can_confirm_profile: false,
  profile_confirmed_at: '2026-11-08T13:00:00Z',
  parents: [summary(dad), summary(mom)],
  children: [summary(emma)],
}

const signedInAsAda = { [ME]: { status: 200, body: ada } }
const signedInAsGrace = { [ME]: { status: 200, body: graceSelf } }

describe('People on Home: your children and your parents', () => {
  test('shows Your children, then Your parents, then the groups, each user opening their page', async () => {
    serve({
      ...signedInAsGrace,
      'GET /api/v2/groups?includes_me=true': page([{ ...family, can_edit: false }]),
      'GET /api/v2/users?group_id=5': page([graceSelf, hedy]),
      'GET /api/v2/users': page([graceSelf, emma, dad, mom, hedy]),
    })
    open('/')

    const people = within(await screen.findByRole('region', { name: 'People' }))
    const parts = await people.findAllByRole('heading', { level: 3 })
    expect(parts.map((h) => h.textContent)).toEqual(['Your children', 'Your parents', 'My family'])
    const children = within(people.getByRole('region', { name: 'Your children' }))
    expect(children.getByRole('link', { name: 'Emma Lovelace' })).toHaveAttribute('href', '/users/9')
    const parents = within(people.getByRole('region', { name: 'Your parents' }))
    expect(parents.getAllByRole('link').map((a) => a.textContent)).toEqual(['Dad Lovelace', 'Mom Lovelace'])
    expect(parents.getByRole('link', { name: 'Mom Lovelace' })).toHaveAttribute('href', '/users/10')
  })

  test('shows them whether or not the user shares a group with them', async () => {
    serve({
      ...signedInAsGrace,
      'GET /api/v2/groups?includes_me=true': page([]),
      'GET /api/v2/users': page([graceSelf, emma, dad, mom]),
    })
    open('/')

    const people = within(await screen.findByRole('region', { name: 'People' }))
    expect(await people.findByRole('region', { name: 'Your children' })).toBeInTheDocument()
    expect(people.getByRole('region', { name: 'Your parents' })).toBeInTheDocument()
  })

  test('shows a child again in the group they share with their parent', async () => {
    serve({
      ...signedInAsGrace,
      'GET /api/v2/groups?includes_me=true': page([{ ...family, can_edit: false }]),
      'GET /api/v2/users?group_id=5': page([graceSelf, emma]),
      'GET /api/v2/users': page([graceSelf, emma, dad, mom]),
    })
    open('/')

    const people = within(await screen.findByRole('region', { name: 'People' }))
    await people.findByRole('region', { name: 'My family' })
    expect(people.getAllByRole('link', { name: 'Emma Lovelace' })).toHaveLength(2)
    expect(within(people.getByRole('region', { name: 'Your children' })).getByRole('link', { name: 'Emma Lovelace' })).toBeInTheDocument()
    expect(within(people.getByRole('region', { name: 'My family' })).getByRole('link', { name: 'Emma Lovelace' })).toBeInTheDocument()
  })

  test("leaves out a parent that GET /users doesn't return, and a part with nobody left", async () => {
    serve({
      ...signedInAsGrace,
      'GET /api/v2/groups?includes_me=true': page([]),
      'GET /api/v2/users': page([graceSelf, emma]),
    })
    open('/')

    const people = within(await screen.findByRole('region', { name: 'People' }))
    expect(await people.findByRole('region', { name: 'Your children' })).toBeInTheDocument()
    expect(people.queryByRole('region', { name: 'Your parents' })).not.toBeInTheDocument()
  })

  test('has no such parts, and asks for no users, for a user with no children and no parents', async () => {
    const fetch = serve({
      ...signedInAsAda,
      'GET /api/v2/groups?includes_me=true': page([]),
    })
    open('/')

    await screen.findByText('Welcome, Ada.')
    expect(screen.queryByRole('region', { name: 'People' })).not.toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users', expect.anything())
  })
})

describe("a user's page: the admin's Parents part", () => {
  const withParents = (parents: User[]): User => ({ ...grace, parents: parents.map(summary) })
  const routes = (body: User) => ({
    ...signedInAsAda,
    'GET /api/v2/users/8': { status: 200, body },
    'GET /api/v2/lists?recipient_id=8&is_primary=false': page([]),
  })

  test('lists the parents with Remove, and has Add parent, in the For part', async () => {
    serve(routes(withParents([dad, mom])))
    open('/users/8')

    const part = within(await screen.findByRole('region', { name: 'For Grace' }))
    expect(part.getByRole('heading', { level: 3, name: 'Parents' })).toBeInTheDocument()
    const items = part.getAllByRole('listitem')
    expect(items.map((li) => li.textContent)).toEqual(['DadRemove', 'MomRemove'])
    expect(part.getByRole('button', { name: 'Remove Dad' })).toBeInTheDocument()
    expect(part.getByRole('link', { name: 'Add parent' })).toHaveAttribute('href', '/users/8/parents')
  })

  test('says so when the user has no parents', async () => {
    serve(routes(withParents([])))
    open('/users/8')

    const part = within(await screen.findByRole('region', { name: 'For Grace' }))
    expect(part.getByText('No parents.')).toBeInTheDocument()
    expect(part.getByRole('link', { name: 'Add parent' })).toBeInTheDocument()
  })

  test('Remove unlinks at once, with no confirmation, and the part shows who is left', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...routes(withParents([dad, mom])),
      'PATCH /api/v2/users/8': (init) => ({
        status: 200,
        body: { ...withParents([mom]), ...(JSON.parse(String(init?.body)) as object), parents: [summary(mom)] },
      }),
    })
    open('/users/8')
    await user.click(await screen.findByRole('button', { name: 'Remove Dad' }))

    await waitFor(() => expect(screen.queryByRole('button', { name: 'Remove Dad' })).not.toBeInTheDocument())
    expect(sentBody(fetch, 'PATCH /api/v2/users/8')).toEqual({ parent_ids: ['10'] })
    expect(screen.getByRole('button', { name: 'Remove Mom' })).toBeInTheDocument()
    expect(window.location.pathname).toBe('/users/8')
  })

  test("shows the server's words when a removal is refused, and keeps the parent", async () => {
    const user = userEvent.setup()
    serve({
      ...routes(withParents([dad])),
      'PATCH /api/v2/users/8': problem(422, 'invalid_input', 'No can do.'),
    })
    open('/users/8')
    await user.click(await screen.findByRole('button', { name: 'Remove Dad' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('No can do.')
    expect(screen.getByRole('button', { name: 'Remove Dad' })).toBeInTheDocument()
  })

  test('is not there for a parent, who can act for the child but not change their parents', async () => {
    serve({
      ...signedInAsGrace,
      'GET /api/v2/users/9': { status: 200, body: { ...grace, id: '9', name: 'Emma', can_edit_parents: false, parents: [summary(graceSelf)] } },
    })
    open('/users/9')

    const part = within(await screen.findByRole('region', { name: 'For Emma' }))
    expect(part.getByRole('link', { name: "Edit Emma's profile" })).toBeInTheDocument()
    expect(part.getByRole('link', { name: 'Sign Emma in' })).toBeInTheDocument()
    expect(part.queryByRole('heading', { name: 'Parents' })).not.toBeInTheDocument()
    expect(part.queryByRole('link', { name: 'Add parent' })).not.toBeInTheDocument()
  })

  test('is the only button in the For part when the admin can edit nothing else', async () => {
    serve(routes({ ...withParents([]), can_edit: false, can_make_login_link: false, can_manage_sessions: false }))
    open('/users/8')

    const part = within(await screen.findByRole('region', { name: 'For Grace' }))
    expect(part.getByRole('link', { name: 'Add parent' })).toBeInTheDocument()
    expect(part.queryByRole('link', { name: "Edit Grace's profile" })).not.toBeInTheDocument()
  })
})

describe('the Add parent screen', () => {
  const asAdmin = (parents: User[]) => ({
    ...signedInAsAda,
    'GET /api/v2/users/8': { status: 200, body: { ...grace, parents: parents.map(summary) } },
    'GET /api/v2/users': page([ada, grace, mom, hedy]),
  })

  test('lists every other user who is not already a parent, by name, and links the one picked', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAdmin([mom]),
      'PATCH /api/v2/users/8': { status: 200, body: { ...grace, parents: [summary(mom), summary(hedy)] } },
    })
    open('/users/8/parents')
    await heading('Add a parent for Grace')

    const rows = await screen.findAllByRole('listitem')
    expect(rows.map((row) => row.textContent)).toEqual(['Ada LovelaceAdd', 'Hedy LamarrAdd'])
    await user.click(screen.getByRole('button', { name: 'Add Hedy Lamarr' }))

    expect(sentBody(fetch, 'PATCH /api/v2/users/8')).toEqual({ parent_ids: ['10', '12'] })
    expect(window.location.pathname).toBe('/users/8')
  })

  test("shows the server's words for a loop, and keeps the list so another can be picked", async () => {
    const user = userEvent.setup()
    serve({
      ...asAdmin([]),
      'PATCH /api/v2/users/8': problem(422, 'parent_loop', "A user can't be their own parent, or a parent of one of their own parents."),
    })
    open('/users/8/parents')
    await heading('Add a parent for Grace')

    await user.click(await screen.findByRole('button', { name: 'Add Hedy Lamarr' }))

    expect(await screen.findByRole('alert')).toHaveTextContent("A user can't be their own parent, or a parent of one of their own parents.")
    expect(screen.getByRole('button', { name: 'Add Mom Lovelace' })).toBeInTheDocument()
    expect(window.location.pathname).toBe('/users/8/parents')
  })

  test('says so when every other user is a parent already', async () => {
    serve({ ...asAdmin([ada, mom, hedy]) })
    open('/users/8/parents')

    expect(await screen.findByText('Every other user is a parent of Grace already.')).toBeInTheDocument()
  })

  test("shows the missing-page message when the caller can't change the user's parents", async () => {
    serve({
      ...signedInAsGrace,
      'GET /api/v2/users/9': { status: 200, body: { ...grace, id: '9', name: 'Emma', can_edit_parents: false } },
    })
    open('/users/9/parents')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('the New user screen: parents', () => {
  const fillIn = async (user: ReturnType<typeof userEvent.setup>) => {
    await user.type(screen.getByLabelText('First name'), 'Emma')
    await user.selectOptions(screen.getByLabelText('Month'), 'March')
    await user.type(screen.getByLabelText('Day'), '4')
  }

  test('offers every user as an optional parent, and sends the ones picked', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...signedInAsAda,
      'GET /api/v2/users': page([ada, mom, hedy]),
      'POST /api/v2/users': { status: 201, body: { ...emma, parents: [summary(mom), summary(hedy)] } },
      'GET /api/v2/users/9': { status: 200, body: emma },
    })
    open('/users/new')
    await heading('New user')

    const parents = within(await screen.findByRole('group', { name: 'Parents (optional)' }))
    expect(parents.getAllByRole('checkbox').map((c) => c.getAttribute('name') ?? c.closest('label')?.textContent)).toEqual([
      'Ada Lovelace',
      'Mom Lovelace',
      'Hedy Lamarr',
    ])
    await fillIn(user)
    await user.click(parents.getByRole('checkbox', { name: 'Mom Lovelace' }))
    await user.click(parents.getByRole('checkbox', { name: 'Hedy Lamarr' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await heading('Emma Lovelace')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/users')).toEqual({
      name: 'Emma',
      birthday: { month: 3, day: 4, year: null },
      parent_ids: ['10', '12'],
    })
  })

  test('sends no parent_ids when none is picked', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...signedInAsAda,
      'GET /api/v2/users': page([ada, mom]),
      'POST /api/v2/users': { status: 201, body: emma },
      'GET /api/v2/users/9': { status: 200, body: emma },
    })
    open('/users/new')
    await heading('New user')

    await screen.findByRole('group', { name: 'Parents (optional)' })
    await fillIn(user)
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await heading('Emma Lovelace')
    expect(sentBody(fetch, 'POST /api/v2/users')).toEqual({ name: 'Emma', birthday: { month: 3, day: 4, year: null } })
  })

  test("shows the server's words when it refuses a parent, and keeps the form", async () => {
    const user = userEvent.setup()
    serve({
      ...signedInAsAda,
      'GET /api/v2/users': page([ada, mom]),
      'POST /api/v2/users': problem(422, 'reference_not_found', "That user doesn't exist, or you can't see them."),
    })
    open('/users/new')
    await heading('New user')

    const parents = within(await screen.findByRole('group', { name: 'Parents (optional)' }))
    await fillIn(user)
    await user.click(parents.getByRole('checkbox', { name: 'Mom Lovelace' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toHaveTextContent("That user doesn't exist, or you can't see them.")
    expect(parents.getByRole('checkbox', { name: 'Mom Lovelace' })).toBeChecked()
  })
})
