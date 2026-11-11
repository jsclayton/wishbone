import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { User } from './api'
import { ada, family, grace, heading, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

/**
 * Makes the answer to a request slow, as it is on a loaded machine. The users of a
 * group arrive after the group, so a test that reads them right after their region
 * appears must wait for them. A test that does not fails every time with this.
 */
function slow(urlEnd: string, ms = 50) {
  const real = globalThis.fetch
  vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input).endsWith(urlEnd)) await new Promise((resolve) => setTimeout(resolve, ms))
    return real(input, init)
  })
}

/** Grace as herself: a user who has confirmed her profile, and is not the admin. */
const graceSelf: User = { ...grace, profile_confirmed_at: '2026-11-08T13:00:00Z', can_edit: true }
const asAda = { [ME]: { status: 200, body: ada } }
const asGrace = { [ME]: { status: 200, body: graceSelf } }
const hedy: User = { ...grace, id: '9', name: 'Hedy', last_name: 'Lamarr' }
/** What a member of a group sees of it: nothing to change. */
const memberOf = (g = family) => ({ ...g, can_edit: false, can_delete: false, can_add_user: false, can_add_group: false })
const inlaws = { ...family, id: '6', name: 'In-laws' }

describe('Menu', () => {
  test('has Groups for everyone', async () => {
    serve(asGrace)
    open('/menu')

    expect(await screen.findByRole('link', { name: 'Groups' })).toHaveAttribute('href', '/groups')
  })

  test('has Groups for the admin, in the part for everyone', async () => {
    serve(asAda)
    open('/menu')

    const mine = (await screen.findByRole('heading', { name: 'Mine' })).parentElement!
    expect(within(mine).getByRole('link', { name: 'Groups' })).toHaveAttribute('href', '/groups')
  })
})

describe('the Groups screen', () => {
  test('lists the groups a user is in, and how they are in each', async () => {
    const through = { ...inlaws, through: { id: '5', name: 'My family' } }
    serve({
      ...asGrace,
      'GET /api/v2/groups': page([memberOf(through), memberOf()]),
    })
    open('/groups')

    expect(await heading('Groups')).toBeInTheDocument()
    const items = await screen.findAllByRole('listitem')
    expect(items.map((item) => item.textContent)).toEqual(["In-lawsYou're in this group through My family.", "My familyYou're in this group."])
    expect(within(items[1]!).getByRole('link', { name: /My family/ })).toHaveAttribute('href', '/groups/5')
    expect(screen.queryByRole('link', { name: 'Create a group' })).not.toBeInTheDocument()
  })

  test('says so, and who to ask, when the user is in no group', async () => {
    serve({ ...asGrace, 'GET /api/v2/groups': page([]) })
    open('/groups')

    expect(await screen.findByRole('heading', { name: 'No groups yet' })).toBeInTheDocument()
    expect(screen.getByText('The admin can add you to one.')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Create a group' })).not.toBeInTheDocument()
  })

  test('shows the admin every group, following the cursor, with a link to create one', async () => {
    const fetch = serve({
      ...asAda,
      'GET /api/v2/groups': page([family], 'abc'),
      'GET /api/v2/groups?cursor=abc': page([inlaws]),
    })
    open('/groups')

    await heading('Groups')
    const items = await screen.findAllByRole('listitem')
    expect(items.map((item) => item.textContent)).toEqual(['My family', 'In-laws'])
    expect(screen.getByRole('link', { name: 'Create a group' })).toHaveAttribute('href', '/groups/new')
    expect(fetch).toHaveBeenCalledWith('/api/v2/groups?cursor=abc', expect.anything())
  })

  test('offers the admin a way to create the first group', async () => {
    serve({ ...asAda, 'GET /api/v2/groups': page([]) })
    open('/groups')

    expect(await screen.findByRole('heading', { name: 'No groups yet' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Create a group' })).toHaveAttribute('href', '/groups/new')
  })
})

describe('the Create a group screen', () => {
  test('makes the group and opens it in place of the form', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'POST /api/v2/groups': { status: 201, body: family },
      'GET /api/v2/groups/5': { status: 200, body: family },
      'GET /api/v2/groups/5/users': page([]),
      'GET /api/v2/groups/5/groups': page([]),
    })
    open('/groups/new')
    await heading('Create a group')

    await user.type(screen.getByLabelText('Group name'), ' My family ')
    await user.click(screen.getByRole('button', { name: 'Create a group' }))

    expect(await heading('My family')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/groups')).toEqual({ name: 'My family' })
    expect(window.location.pathname).toBe('/groups/5')
  })

  test('asks for a name', async () => {
    const user = userEvent.setup()
    const fetch = serve(asAda)
    open('/groups/new')
    await heading('Create a group')

    await user.click(screen.getByRole('button', { name: 'Create a group' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Enter a group name.')
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  test("shows the server's words when it refuses the name", async () => {
    const user = userEvent.setup()
    serve({ ...asAda, 'POST /api/v2/groups': problem(422, 'invalid_input', 'The name must be at most 100 characters.') })
    open('/groups/new')
    await heading('Create a group')

    await user.type(screen.getByLabelText('Group name'), 'x')
    await user.click(screen.getByRole('button', { name: 'Create a group' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('The name must be at most 100 characters.')
  })

  test('shows the missing-page message to anyone but the admin, and asks for nothing', async () => {
    const fetch = serve(asGrace)
    open('/groups/new')

    expect(await heading("We can't find that page")).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})

describe('a group', () => {
  const listed = (g: typeof family, users: User[], inner: (typeof family)[] = []) => ({
    ...asAda,
    [`GET /api/v2/groups/${g.id}`]: { status: 200, body: g },
    [`GET /api/v2/groups/${g.id}/users`]: page(users),
    [`GET /api/v2/groups/${g.id}/groups`]: page(inner),
  })

  test('shows its users, each opening their page, to a user in it, with nothing to change', async () => {
    const fetch = serve({
      ...asGrace,
      'GET /api/v2/groups/5': { status: 200, body: memberOf() },
      'GET /api/v2/groups/5/users': page([graceSelf, hedy]),
    })
    slow('/groups/5/users')
    open('/groups/5')

    expect(await heading('My family')).toBeInTheDocument()
    const users = within(await screen.findByRole('region', { name: 'Users' }))
    expect((await users.findAllByRole('listitem')).map((item) => item.textContent)).toEqual(['Grace Hopper (you)', 'Hedy Lamarr'])
    expect(users.getByRole('link', { name: 'Hedy Lamarr' })).toHaveAttribute('href', '/users/9')
    expect(screen.queryByRole('button', { name: /^Remove/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Rename' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Add a user' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Delete this group' })).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Groups inside this one' })).not.toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/groups/5/groups', expect.anything())
  })

  test("shows the admin the group's users and groups, with what to do with each", async () => {
    serve(listed(family, [graceSelf], [inlaws]))
    slow('/groups/5/users')
    open('/groups/5')

    await heading('My family')
    const users = within(await screen.findByRole('region', { name: 'Users' }))
    expect(await users.findByRole('button', { name: 'Remove Grace Hopper' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Add a user' })).toHaveAttribute('href', '/groups/5/users')
    const inside = within(await screen.findByRole('region', { name: 'Groups inside this one' }))
    expect(inside.getAllByRole('listitem').map((item) => item.textContent)).toEqual(['In-lawsRemove'])
    expect(screen.getByRole('link', { name: 'Add a group' })).toHaveAttribute('href', '/groups/5/groups')
    expect(screen.getByRole('link', { name: 'Delete this group' })).toHaveAttribute('href', '/groups/5/delete')
  })

  test('shows the missing-page message for a group the user is not in', async () => {
    serve({ ...asGrace, 'GET /api/v2/groups/5': problem(404, 'not_found') })
    open('/groups/5')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })

  test('is renamed with a form that starts with the name, and Cancel changes nothing', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...listed(family, []),
      'PATCH /api/v2/groups/5': { status: 200, body: { ...family, name: 'Family' } },
    })
    open('/groups/5')
    await heading('My family')

    await user.click(screen.getByRole('button', { name: 'Rename' }))
    expect(screen.getByLabelText('Group name')).toHaveValue('My family')
    await user.clear(screen.getByLabelText('Group name'))
    await user.type(screen.getByLabelText('Group name'), 'Changed my mind')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(await heading('My family')).toBeInTheDocument()
    expect(screen.queryByLabelText('Group name')).not.toBeInTheDocument()
    expect(fetch.mock.calls.some(([, init]) => init?.method === 'PATCH')).toBe(false)

    await user.click(screen.getByRole('button', { name: 'Rename' }))
    await user.clear(screen.getByLabelText('Group name'))
    await user.type(screen.getByLabelText('Group name'), ' Family ')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await heading('Family')).toBeInTheDocument()
    expect(sentBody(fetch, 'PATCH /api/v2/groups/5')).toEqual({ name: 'Family' })
    const patch = fetch.mock.calls.find(([, init]) => init?.method === 'PATCH')!
    expect(new Headers(patch[1]?.headers).get('Content-Type')).toBe('application/merge-patch+json')
    expect(screen.queryByLabelText('Group name')).not.toBeInTheDocument()
  })

  test("keeps the form open with the server's words when it refuses the name", async () => {
    const user = userEvent.setup()
    serve({ ...listed(family, []), 'PATCH /api/v2/groups/5': problem(422, 'invalid_input', 'The name is required.') })
    open('/groups/5')
    await heading('My family')

    await user.click(screen.getByRole('button', { name: 'Rename' }))
    await user.clear(screen.getByLabelText('Group name'))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('The name is required.')
    expect(screen.getByLabelText('Group name')).toBeInTheDocument()
  })

  test('loses a user that the admin removes', async () => {
    const user = userEvent.setup()
    let removed = false
    serve({
      ...asAda,
      'GET /api/v2/groups/5': { status: 200, body: family },
      'GET /api/v2/groups/5/users': () => page(removed ? [hedy] : [graceSelf, hedy]),
      'GET /api/v2/groups/5/groups': page([]),
      'DELETE /api/v2/groups/5/users/8': () => {
        removed = true
        return { status: 204 }
      },
    })
    open('/groups/5')
    await heading('My family')

    await user.click(await screen.findByRole('button', { name: 'Remove Grace Hopper' }))

    const users = within(screen.getByRole('region', { name: 'Users' }))
    await waitFor(() => expect(users.queryByText('Grace Hopper')).not.toBeInTheDocument())
    expect(users.getByText('Hedy Lamarr')).toBeInTheDocument()
  })

  test('loses a group that the admin takes out', async () => {
    const user = userEvent.setup()
    let removed = false
    serve({
      ...asAda,
      'GET /api/v2/groups/5': { status: 200, body: family },
      'GET /api/v2/groups/5/users': page([]),
      'GET /api/v2/groups/5/groups': () => page(removed ? [] : [inlaws]),
      'DELETE /api/v2/groups/5/groups/6': () => {
        removed = true
        return { status: 204 }
      },
    })
    open('/groups/5')
    await heading('My family')

    await user.click(await screen.findByRole('button', { name: 'Remove In-laws' }))

    expect(await screen.findByText('No groups inside yet.')).toBeInTheDocument()
  })

  test('says so when a removal fails, and keeps the user', async () => {
    const user = userEvent.setup()
    serve({ ...listed(family, [graceSelf]), 'DELETE /api/v2/groups/5/users/8': problem(500, 'internal_error') })
    open('/groups/5')
    await heading('My family')

    await user.click(await screen.findByRole('button', { name: 'Remove Grace Hopper' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(screen.getByText('Grace Hopper')).toBeInTheDocument()
  })
})

describe('the Add a user screen', () => {
  test('lists the users who are not in the group, and adds the one the admin picks', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'GET /api/v2/groups/5': { status: 200, body: family },
      'GET /api/v2/groups/5/users': page([graceSelf]),
      'GET /api/v2/users': page([ada, graceSelf, hedy]),
      'POST /api/v2/groups/5/users': { status: 201, body: hedy },
      'GET /api/v2/groups/5/groups': page([]),
    })
    open('/groups/5/users')
    await heading('Add a user to My family')

    const rows = await screen.findAllByRole('listitem')
    expect(rows.map((row) => row.textContent)).toEqual(['Ada Lovelace (you)Add', 'Hedy LamarrAdd'])
    await user.click(screen.getByRole('button', { name: 'Add Hedy Lamarr' }))

    expect(sentBody(fetch, 'POST /api/v2/groups/5/users')).toEqual({ user_id: '9' })
    expect(await heading('My family')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/groups/5')
  })

  test('says so when everyone is in the group already', async () => {
    serve({
      ...asAda,
      'GET /api/v2/groups/5': { status: 200, body: family },
      'GET /api/v2/groups/5/users': page([ada]),
      'GET /api/v2/users': page([ada]),
    })
    open('/groups/5/users')

    expect(await screen.findByText('Everyone is in My family already.')).toBeInTheDocument()
  })

  test("shows the server's words when it refuses, and keeps the list", async () => {
    const user = userEvent.setup()
    serve({
      ...asAda,
      'GET /api/v2/groups/5': { status: 200, body: family },
      'GET /api/v2/groups/5/users': page([]),
      'GET /api/v2/users': page([hedy]),
      'POST /api/v2/groups/5/users': problem(409, 'already_in_group', 'That is already directly in this group.'),
    })
    open('/groups/5/users')
    await heading('Add a user to My family')

    await user.click(await screen.findByRole('button', { name: 'Add Hedy Lamarr' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('That is already directly in this group.')
    expect(screen.getByRole('button', { name: 'Add Hedy Lamarr' })).toBeInTheDocument()
  })

  test('shows the missing-page message to a user who cannot add users', async () => {
    serve({ ...asGrace, 'GET /api/v2/groups/5': { status: 200, body: memberOf() } })
    open('/groups/5/users')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('the Add a group screen', () => {
  const others = {
    ...asAda,
    'GET /api/v2/groups/5': { status: 200, body: family },
    'GET /api/v2/groups/5/groups': page([]),
    'GET /api/v2/groups': page([family, inlaws]),
  }

  test('lists the other groups, and nests the one the admin picks', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...others,
      'POST /api/v2/groups/5/groups': { status: 201, body: inlaws },
      'GET /api/v2/groups/5/users': page([]),
    })
    open('/groups/5/groups')
    await heading('Add a group to My family')

    const rows = await screen.findAllByRole('listitem')
    expect(rows.map((row) => row.textContent)).toEqual(['In-lawsAdd'])
    await user.click(screen.getByRole('button', { name: 'Add In-laws' }))

    expect(sentBody(fetch, 'POST /api/v2/groups/5/groups')).toEqual({ group_id: '6' })
    expect(await heading('My family')).toBeInTheDocument()
  })

  test('does not offer a group that is already inside', async () => {
    serve({ ...others, 'GET /api/v2/groups/5/groups': page([inlaws]) })
    open('/groups/5/groups')

    expect(await screen.findByText('Every other group is inside My family already.')).toBeInTheDocument()
  })

  test('explains a loop in its own words, and keeps the list', async () => {
    const user = userEvent.setup()
    serve({ ...others, 'POST /api/v2/groups/5/groups': problem(422, 'group_loop', 'That would put a group inside itself.') })
    open('/groups/5/groups')
    await heading('Add a group to My family')

    await user.click(await screen.findByRole('button', { name: 'Add In-laws' }))

    expect(await screen.findByRole('alert')).toHaveTextContent("Can't add In-laws. My family is already inside In-laws, and a group can't end up inside itself.")
    expect(screen.getByRole('button', { name: 'Add In-laws' })).toBeInTheDocument()
  })
})

describe('the Delete this group screen', () => {
  const deletable = {
    ...asAda,
    'GET /api/v2/groups/5': { status: 200, body: family },
    'GET /api/v2/groups/5/users': page([]),
    'GET /api/v2/groups/5/groups': page([]),
  }

  test('asks first, and Keep it goes back to the group', async () => {
    const user = userEvent.setup()
    serve(deletable)
    open('/groups/5/delete')

    expect(await heading('Delete My family?')).toBeInTheDocument()
    await user.click(screen.getByRole('link', { name: 'Keep it' }))

    expect(await heading('My family')).toBeInTheDocument()
  })

  test('deletes the group and opens Groups, in place of the question', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...deletable,
      'DELETE /api/v2/groups/5': { status: 204 },
      'GET /api/v2/groups': page([]),
    })
    open('/groups/5/delete')
    await heading('Delete My family?')

    await user.click(screen.getByRole('button', { name: 'Delete this group' }))

    expect(await heading('Groups')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/groups/5', expect.objectContaining({ method: 'DELETE' }))
    expect(window.location.pathname).toBe('/groups')
  })

  test('shows the missing-page message to a user who cannot delete it', async () => {
    serve({ ...asGrace, 'GET /api/v2/groups/5': { status: 200, body: memberOf() } })
    open('/groups/5/delete')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('People on Home', () => {
  test('shows an outer group with the users in the groups inside it, which the filter returns', async () => {
    const fetch = serve({
      ...asGrace,
      'GET /api/v2/groups?includes_me=true': page([memberOf({ ...inlaws, through: { id: '5', name: 'My family' } })]),
      // The server returns the users in In-laws at any depth, the caller included.
      'GET /api/v2/users?group_id=6': page([graceSelf, hedy]),
    })
    open('/')

    const group = within(await screen.findByRole('region', { name: 'In-laws' }))
    expect(group.getByText('through My family')).toBeInTheDocument()
    expect(group.getAllByRole('link').map((a) => a.textContent)).toEqual(['Hedy Lamarr'])
    expect(fetch).toHaveBeenCalledWith('/api/v2/users?group_id=6', expect.anything())
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/groups/6/users', expect.anything())
  })

  test("shows each of the user's groups with the other users in it, each opening their page", async () => {
    const through = { ...inlaws, through: { id: '5', name: 'My family' } }
    serve({
      ...asGrace,
      'GET /api/v2/groups?includes_me=true': page([memberOf(through), memberOf()]),
      'GET /api/v2/users?group_id=5': page([graceSelf, hedy]),
      'GET /api/v2/users?group_id=6': page([{ ...hedy, id: '11', name: 'Ivy', last_name: null }]),
    })
    open('/')

    const people = within(await screen.findByRole('region', { name: 'People' }))
    const inLaws = within(await people.findByRole('region', { name: 'In-laws' }))
    expect(inLaws.getByText('through My family')).toBeInTheDocument()
    expect(inLaws.getByRole('link', { name: 'Ivy' })).toHaveAttribute('href', '/users/11')
    const mine = within(people.getByRole('region', { name: 'My family' }))
    expect(mine.getAllByRole('link').map((a) => a.textContent)).toEqual(['Hedy Lamarr'])
    expect(mine.getByRole('link', { name: 'Hedy Lamarr' })).toHaveAttribute('href', '/users/9')
    expect(mine.queryByText(/through/)).not.toBeInTheDocument()
  })

  test('leaves out a group with nobody else in it, and the whole part when there are none', async () => {
    serve({
      ...asGrace,
      'GET /api/v2/groups?includes_me=true': page([memberOf()]),
      'GET /api/v2/users?group_id=5': page([graceSelf]),
    })
    open('/')

    await heading('Home')
    await waitFor(() => expect(screen.getByText(/Welcome, Grace/)).toBeInTheDocument())
    expect(screen.queryByRole('region', { name: 'People' })).not.toBeInTheDocument()
  })

  test("asks for the admin's own groups only", async () => {
    const fetch = serve({
      ...asAda,
      'GET /api/v2/groups?includes_me=true': page([]),
    })
    open('/')

    await heading('Home')
    await waitFor(() => expect(fetch).toHaveBeenCalledWith('/api/v2/groups?includes_me=true', expect.anything()))
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/groups', expect.anything())
  })

  test('says so when the people cannot be loaded', async () => {
    serve({ ...asGrace, 'GET /api/v2/groups?includes_me=true': problem(500, 'internal_error') })
    open('/')

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load people. Reload and try again.')
  })
})
