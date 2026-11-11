import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Group, Occasion, User } from './api'
import { aGift, ada, family, grace, heading, ME, open, page, problem, sentBody, serve } from './test/fake'

// Today is Nov 8 2026.
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(2026, 10, 8, 12))
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const asAda = { [ME]: { status: 200, body: ada } }
const asGrace = { [ME]: { status: 200, body: { ...grace, can_edit: true } } }

const rule = (month: number, day: number): Occasion['rule'] => ({ kind: 'annual', month, day, year: null, weekday: null, n: null })

const shared = (id: string, name: string, month: number, day: number, next: string | null, more: Partial<Occasion> = {}): Occasion => ({
  id,
  kind: 'shared',
  name,
  rule: rule(month, day),
  user: null,
  is_everyone: false,
  next_date: next,
  can_edit: true,
  can_delete: true,
  can_attach_group: true,
  ...more,
})

const christmas = shared('20', 'Christmas', 12, 25, '2026-12-25')
const newYear = shared('21', 'New Year', 1, 1, '2027-01-01', { is_everyone: true, can_attach_group: false })
const asMember = (o: Occasion): Occasion => ({ ...o, can_edit: false, can_delete: false, can_attach_group: false })

const aunts: Group = { ...family, id: '5', name: 'Aunts', through: null }
const inlaws: Group = { ...family, id: '6', name: 'In-laws', through: null }
const hedy: User = { ...grace, id: '9', name: 'Hedy', last_name: 'Lamarr' }

const empty = page([])

describe('Menu', () => {
  test('has Shared occasions in the admin part', async () => {
    serve(asAda)
    open('/menu')

    const admin = (await screen.findByRole('heading', { name: 'For the admin' })).parentElement!
    expect(within(admin).getByRole('link', { name: 'Shared occasions' })).toHaveAttribute('href', '/occasions')
  })

  test('has none for anyone else', async () => {
    serve(asGrace)
    open('/menu')

    await heading('Menu')
    expect(screen.queryByRole('link', { name: 'Shared occasions' })).not.toBeInTheDocument()
  })
})

describe('the Shared occasions screen', () => {
  test('lists the shared occasions by name, marks the ones for everyone, and opens each', async () => {
    serve({ ...asAda, 'GET /api/v2/occasions?kind=shared&sort=name': page([christmas, newYear]) })
    open('/occasions')

    expect(await heading('Shared occasions')).toBeInTheDocument()
    const rows = await screen.findAllByRole('listitem')
    expect(rows.map((li) => li.textContent)).toEqual(['ChristmasEvery year on December 25', 'New YearEvery year on January 1Everyone'])
    expect(within(rows[0]!).getByRole('link')).toHaveAttribute('href', '/occasions/20')
    expect(screen.getByRole('link', { name: 'New shared occasion' })).toHaveAttribute('href', '/occasions/new')
  })

  test('says so when there are none', async () => {
    serve({ ...asAda, 'GET /api/v2/occasions?kind=shared&sort=name': empty })
    open('/occasions')

    expect(await screen.findByText('No shared occasions yet.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'New shared occasion' })).toBeInTheDocument()
  })

  test('is the missing-page message for anyone but the admin', async () => {
    serve(asGrace)
    open('/occasions')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('New shared occasion', () => {
  test('sends the name, the rule and whether it is everyone, and opens the new occasion', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'POST /api/v2/occasions': { status: 201, body: christmas },
      'GET /api/v2/occasions/20': { status: 200, body: christmas },
      'GET /api/v2/groups?includes_me=true': empty,
      'GET /api/v2/occasions/20/groups': empty,
    })
    open('/occasions/new')

    expect(await heading('New shared occasion')).toBeInTheDocument()
    await user.type(screen.getByLabelText("What's it called?"), ' Christmas ')
    await user.selectOptions(screen.getByLabelText('Month'), 'December')
    await user.type(screen.getByLabelText('Day'), '25')
    await user.click(screen.getByRole('checkbox', { name: /Everyone/ }))
    await user.click(screen.getByRole('button', { name: 'Create this shared occasion' }))

    expect(await heading('Christmas')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/occasions')).toEqual({
      kind: 'shared',
      name: 'Christmas',
      is_everyone: true,
      rule: { kind: 'annual', month: 12, day: 25 },
    })
    expect(window.location.pathname).toBe('/occasions/20')
  })

  test('is not everyone unless the switch is on', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'POST /api/v2/occasions': { status: 201, body: christmas },
      'GET /api/v2/occasions/20': { status: 200, body: christmas },
      'GET /api/v2/groups?includes_me=true': empty,
      'GET /api/v2/occasions/20/groups': empty,
    })
    open('/occasions/new')

    await heading('New shared occasion')
    await user.type(screen.getByLabelText("What's it called?"), 'Christmas')
    await user.selectOptions(screen.getByLabelText('Month'), 'December')
    await user.type(screen.getByLabelText('Day'), '25')
    await user.click(screen.getByRole('button', { name: 'Create this shared occasion' }))

    await heading('Christmas')
    expect(sentBody(fetch, 'POST /api/v2/occasions')).toMatchObject({ is_everyone: false })
  })

  test('shows the words of a refusal and stays', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, 'POST /api/v2/occasions': problem(422, 'invalid_input', 'That date is not in the calendar.') })
    open('/occasions/new')

    await heading('New shared occasion')
    await user.type(screen.getByLabelText("What's it called?"), 'Odd')
    await user.selectOptions(screen.getByLabelText('Month'), 'February')
    await user.type(screen.getByLabelText('Day'), '30')
    await user.click(screen.getByRole('button', { name: 'Create this shared occasion' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('That date is not in the calendar.')
    expect(window.location.pathname).toBe('/occasions/new')
  })

  test('is the missing-page message for anyone but the admin', async () => {
    serve(asGrace)
    open('/occasions/new')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('a shared occasion', () => {
  // Grace is in Aunts, and Hedy is too. In-laws holds nobody else.
  const onIt = (o: Occasion, more = {}) => ({
    [`GET /api/v2/occasions/${o.id}`]: { status: 200, body: o },
    'GET /api/v2/groups?includes_me=true': page([aunts, { ...inlaws, through: { id: '5', name: 'Aunts' } }]),
    [`GET /api/v2/users?occasion_id=${o.id}&group_id=5`]: page([{ ...grace, can_edit: false }, hedy]),
    [`GET /api/v2/users?occasion_id=${o.id}&group_id=6`]: page([{ ...grace, can_edit: false }]),
    ...more,
  })

  test('shows its date with a countdown, and the people on it by the groups of the user who looks', async () => {
    serve({ ...asGrace, ...onIt(asMember(christmas)) })
    open('/occasions/20')

    expect(await heading('Christmas')).toBeInTheDocument()
    expect(await screen.findByText(/Fri Dec 25/)).toHaveTextContent('Fri Dec 25, 47 days')
    const section = screen.getByRole('heading', { name: "Who's on it" }).closest('section')!
    // The user is left out of their own groups, and a group with nobody else is left out.
    const aunt = await within(section).findByRole('region', { name: 'Aunts' })
    expect(within(aunt).getAllByRole('link').map((a) => a.textContent)).toEqual(['Hedy Lamarr'])
    expect(within(aunt).getByRole('link', { name: 'Hedy Lamarr' })).toHaveAttribute('href', '/users/9')
    expect(within(section).queryByRole('region', { name: 'In-laws' })).not.toBeInTheDocument()
    expect(within(aunt).getByRole('link', { name: 'Hedy Lamarr' })).not.toHaveTextContent('from you')
    expect(within(aunt).getByRole('link', { name: 'Hedy Lamarr' })).not.toHaveTextContent('Nothing yet')
    // Nothing to change, and the groups are the admin's.
    expect(screen.queryByRole('link', { name: /Edit/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Groups' })).not.toBeInTheDocument()
  })

  test('marks each user with how many of the giver\'s own open gifts are for them on this date, or Nothing yet', async () => {
    const forHedy = (id: string, date = '2026-12-25') => ({
      ...aGift,
      id,
      occasion: { id: '20', kind: 'shared' as const, name: 'Christmas' },
      occasion_date: date,
      recipients: [{ id: '9', name: 'Hedy', avatar: null }],
    })
    serve({
      ...asGrace,
      ...onIt(asMember(christmas), {
        'GET /api/v2/gifts?giver_id=me&state=open&occasion_id=20': page([forHedy('1'), forHedy('2'), forHedy('3', '2025-12-25')]),
        'GET /api/v2/users?occasion_id=20&group_id=6': page([{ ...grace, can_edit: false }, hedy]),
      }),
    })
    open('/occasions/20')

    const aunt = await screen.findByRole('region', { name: 'Aunts' })
    expect(await within(aunt).findByRole('link', { name: /Hedy Lamarr/ })).toHaveTextContent('Hedy Lamarr2 from you')
    // Only this date counts: the gift for last year's Christmas is not one of them.
    const inlawsPart = screen.getByRole('region', { name: 'In-laws' })
    expect(within(inlawsPart).getAllByRole('link').map((a) => a.textContent)).toEqual(['Hedy Lamarr2 from you'])
  })

  test('does not count the giver when the giver is also a recipient of the list', async () => {
    const forBoth = {
      ...aGift,
      id: '1',
      occasion: { id: '20', kind: 'shared' as const, name: 'Christmas' },
      occasion_date: '2026-12-25',
      recipients: [
        { id: '8', name: 'Grace', avatar: null },
        { id: '9', name: 'Hedy', avatar: null },
      ],
    }
    serve({ ...asGrace, ...onIt(asMember(christmas), { 'GET /api/v2/gifts?giver_id=me&state=open&occasion_id=20': page([forBoth]) }) })
    open('/occasions/20')

    const aunt = await screen.findByRole('region', { name: 'Aunts' })
    expect(await within(aunt).findByRole('link', { name: /Hedy Lamarr/ })).toHaveTextContent('Hedy Lamarr1 from you')
  })

  test('says Nothing yet for a user nothing is given to', async () => {
    serve({ ...asGrace, ...onIt(asMember(christmas), { 'GET /api/v2/gifts?giver_id=me&state=open&occasion_id=20': page([]) }) })
    open('/occasions/20')

    const aunt = await screen.findByRole('region', { name: 'Aunts' })
    expect(await within(aunt).findByRole('link', { name: /Hedy Lamarr/ })).toHaveTextContent('Hedy LamarrNothing yet')
  })

  test('notes the group a group is reached through, as People does', async () => {
    serve({ ...asGrace, ...onIt(asMember(christmas), { 'GET /api/v2/users?occasion_id=20&group_id=6': page([hedy]) }) })
    open('/occasions/20')

    const inlawsPart = await screen.findByRole('region', { name: 'In-laws' })
    expect(inlawsPart).toHaveTextContent('through Aunts')
  })

  test('shows a child and a parent who are on it, from the links, whether or not they share a group', async () => {
    const emma = { ...hedy, id: '10', name: 'Emma', last_name: 'Hopper' }
    const nan = { ...hedy, id: '11', name: 'Nan', last_name: 'Hopper' }
    const mother: User = { ...grace, can_edit: true, children: [{ id: '10', name: 'Emma', avatar: null }], parents: [{ id: '11', name: 'Nan', avatar: null }] }
    serve({
      [ME]: { status: 200, body: mother },
      ...onIt(asMember(christmas), {
        'GET /api/v2/users?occasion_id=20': page([mother, emma, nan, hedy]),
        'GET /api/v2/groups?includes_me=true': page([]),
      }),
    })
    open('/occasions/20')

    const section = (await screen.findByRole('heading', { name: "Who's on it" })).closest('section')!
    const children = await within(section).findByRole('region', { name: 'Your children' })
    expect(within(children).getAllByRole('link').map((a) => a.textContent)).toEqual(['Emma Hopper'])
    const parents = within(section).getByRole('region', { name: 'Your parents' })
    expect(within(parents).getAllByRole('link').map((a) => a.textContent)).toEqual(['Nan Hopper'])
  })

  test('leaves out a child the user can not see on it, and asks for the users only with a link', async () => {
    const mother: User = { ...grace, can_edit: true, children: [{ id: '10', name: 'Emma', avatar: null }] }
    const fetch = serve({
      [ME]: { status: 200, body: mother },
      ...onIt(asMember(christmas), { 'GET /api/v2/users?occasion_id=20': page([mother]), 'GET /api/v2/groups?includes_me=true': page([]) }),
    })
    open('/occasions/20')

    expect(await screen.findByText('Nobody else you can see is on this yet.')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Your children' })).not.toBeInTheDocument()
    expect(fetch.mock.calls.map(([url]) => String(url))).toContain('/api/v2/users?occasion_id=20')
  })

  test('shows the admin only the admin\'s own groups, as People does', async () => {
    const fetch = serve({ ...asAda, ...onIt(christmas, { 'GET /api/v2/occasions/20/groups': page([aunts]), 'GET /api/v2/groups?includes_me=true': page([inlaws]) }) })
    open('/occasions/20')

    await heading('Christmas')
    await waitFor(() => expect(fetch.mock.calls.map(([url]) => String(url))).toContain('/api/v2/users?occasion_id=20&group_id=6'))
    expect(fetch.mock.calls.map(([url]) => String(url))).not.toContain('/api/v2/users?occasion_id=20&group_id=5')
  })

  test('says so when nobody else the user can see is on it', async () => {
    serve({ ...asGrace, ...onIt(asMember(christmas), { 'GET /api/v2/groups?includes_me=true': empty }) })
    open('/occasions/20')

    await heading('Christmas')
    expect(await screen.findByText('Nobody else you can see is on this yet.')).toBeInTheDocument()
  })

  test('says Today on the day, and Already passed for a one-time date that has gone', async () => {
    serve({ ...asGrace, ...onIt(asMember(shared('22', 'Founders Day', 11, 8, '2026-11-08'))) })
    open('/occasions/22')

    await heading('Founders Day')
    expect(await screen.findByText(/Sun Nov 8/)).toHaveTextContent('Sun Nov 8, Today')
  })

  test('says Already passed for a one-time date that has gone', async () => {
    const gone: Occasion = { ...asMember(christmas), id: '23', name: 'Old news', rule: { ...rule(5, 5), kind: 'once', year: 2020 }, next_date: null }
    serve({ ...asGrace, ...onIt(gone) })
    open('/occasions/23')

    await heading('Old news')
    expect(await screen.findByText('Already passed')).toBeInTheDocument()
  })

  test('is the missing-page message for a birthday or a custom occasion', async () => {
    serve({ ...asGrace, ...onIt({ ...asMember(christmas), kind: 'birthday', user: { id: '9', name: 'Hedy', avatar: null }, is_everyone: null }) })
    open('/occasions/20')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })

  test('shows the admin its groups, with Detach, and a way to attach another', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      ...onIt(christmas, { 'GET /api/v2/occasions/20/groups': page([aunts]), 'DELETE /api/v2/occasions/20/groups/5': { status: 204 } }),
    })
    open('/occasions/20')

    await heading('Christmas')
    const groups = (await screen.findByRole('heading', { name: 'Groups' })).closest('section')!
    expect(within(groups).getByText('Aunts')).toBeInTheDocument()
    expect(within(groups).getByRole('link', { name: 'Attach a group' })).toHaveAttribute('href', '/occasions/20/groups')
    expect(screen.getByRole('link', { name: 'Edit this occasion' })).toHaveAttribute('href', '/occasions/20/edit')
    expect(screen.getByRole('link', { name: 'Delete this occasion' })).toHaveAttribute('href', '/occasions/20/delete')

    await user.click(within(groups).getByRole('button', { name: 'Detach Aunts' }))
    await waitFor(() => expect(fetch).toHaveBeenCalledWith('/api/v2/occasions/20/groups/5', expect.objectContaining({ method: 'DELETE' })))
  })

  test('says Everyone, and offers no group, when it is marked everyone', async () => {
    serve({ ...asAda, ...onIt(newYear) })
    open('/occasions/21')

    await heading('New Year')
    expect(await screen.findByText('Everyone is on this occasion, including users who join later.')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Attach a group' })).not.toBeInTheDocument()
  })
})

describe('Edit a shared occasion', () => {
  test('is filled in, and saves the name and the rule', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'GET /api/v2/occasions/20': { status: 200, body: christmas },
      'PATCH /api/v2/occasions/20': { status: 200, body: christmas },
      'GET /api/v2/groups?includes_me=true': empty,
      'GET /api/v2/occasions/20/groups': empty,
    })
    open('/occasions/20/edit')

    expect(await heading('Edit a shared occasion')).toBeInTheDocument()
    expect(screen.getByLabelText("What's it called?")).toHaveValue('Christmas')
    expect(screen.getByRole('checkbox', { name: /Everyone/ })).not.toBeChecked()
    await user.clear(screen.getByLabelText("What's it called?"))
    await user.type(screen.getByLabelText("What's it called?"), 'Xmas')
    await user.click(screen.getByRole('button', { name: 'Save this shared occasion' }))

    expect(await heading('Christmas')).toBeInTheDocument()
    expect(sentBody(fetch, 'PATCH /api/v2/occasions/20')).toEqual({ name: 'Xmas', rule: { kind: 'annual', month: 12, day: 25 } })
    expect(window.location.pathname).toBe('/occasions/20')
  })

  test('marks it everyone, and warns that its groups are detached', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'GET /api/v2/occasions/20': { status: 200, body: christmas },
      'PATCH /api/v2/occasions/20': { status: 200, body: { ...christmas, is_everyone: true, can_attach_group: false } },
      'GET /api/v2/groups?includes_me=true': empty,
    })
    open('/occasions/20/edit')

    await heading('Edit a shared occasion')
    expect(screen.getByText('Marking it everyone detaches its groups.')).toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: /Everyone/ }))
    await user.click(screen.getByRole('button', { name: 'Save this shared occasion' }))

    await waitFor(() => expect(sentBody(fetch, 'PATCH /api/v2/occasions/20')).toMatchObject({ is_everyone: true }))
  })

  test('unmarks it, and says it is then attached to no groups', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'GET /api/v2/occasions/21': { status: 200, body: newYear },
      'PATCH /api/v2/occasions/21': { status: 200, body: { ...newYear, is_everyone: false, can_attach_group: true } },
      'GET /api/v2/groups?includes_me=true': empty,
      'GET /api/v2/occasions/21/groups': empty,
    })
    open('/occasions/21/edit')

    await heading('Edit a shared occasion')
    expect(screen.getByRole('checkbox', { name: /Everyone/ })).toBeChecked()
    expect(screen.getByText('Unmarking it leaves it attached to no groups.')).toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: /Everyone/ }))
    await user.click(screen.getByRole('button', { name: 'Save this shared occasion' }))

    await waitFor(() => expect(sentBody(fetch, 'PATCH /api/v2/occasions/21')).toMatchObject({ is_everyone: false }))
  })

  test('is the missing-page message for a user who cannot change it', async () => {
    serve({ ...asGrace, 'GET /api/v2/occasions/20': { status: 200, body: asMember(christmas) } })
    open('/occasions/20/edit')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('Delete a shared occasion', () => {
  test('asks first, then deletes and goes back to Shared occasions', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'GET /api/v2/occasions/20': { status: 200, body: christmas },
      'DELETE /api/v2/occasions/20': { status: 204 },
      'GET /api/v2/occasions?kind=shared&sort=name': page([newYear]),
    })
    open('/occasions/20/delete')

    expect(await heading('Delete Christmas?')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Keep it' })).toHaveAttribute('href', '/occasions/20')
    await user.click(screen.getByRole('button', { name: 'Delete this occasion' }))

    expect(await heading('Shared occasions')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/occasions/20', expect.objectContaining({ method: 'DELETE' }))
    expect(window.location.pathname).toBe('/occasions')
  })
})

describe('Attach a group', () => {
  test('lists the groups that are not attached, and attaches the one picked', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'GET /api/v2/occasions/20': { status: 200, body: christmas },
      'GET /api/v2/occasions/20/groups': page([aunts]),
      'GET /api/v2/groups': page([aunts, inlaws]),
      'POST /api/v2/occasions/20/groups': { status: 201, body: inlaws },
      'GET /api/v2/groups?includes_me=true': empty,
    })
    open('/occasions/20/groups')

    expect(await heading('Attach a group to Christmas')).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: 'Add In-laws' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Aunts' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Add In-laws' }))

    expect(await heading('Christmas')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/occasions/20/groups')).toEqual({ group_id: '6' })
  })

  test('is the missing-page message when no group can be attached to it', async () => {
    serve({ ...asAda, 'GET /api/v2/occasions/21': { status: 200, body: newYear } })
    open('/occasions/21/groups')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('the occasions on the profile screen', () => {
  test('lists a shared occasion beside the others, which opens who is on it, and has no Edit for it', async () => {
    serve({
      ...asAda,
      'GET /api/v2/users/7': { status: 200, body: ada },
      'GET /api/v2/occasions?user_id=7': page([christmas]),
    })
    open('/users/7/profile')

    const list = await screen.findByRole('list', { name: 'Occasions' })
    const row = within(list).getByRole('listitem')
    expect(row).toHaveTextContent('Dec 25, 2026ChristmasShared occasionSee who\'s on it')
    expect(within(row).getByRole('link', { name: "See who's on it Christmas" })).toHaveAttribute('href', '/occasions/20')
    expect(within(row).queryByRole('link', { name: /Edit/ })).not.toBeInTheDocument()
  })
})
