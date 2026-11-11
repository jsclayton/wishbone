import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Occasion, User } from './api'
import { ada, grace, heading, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

// Ada is signed in. Her birthday is Dec 10. Grace, as the admin sees her, has
// her birthday occasion and, through a parent link, nothing more.
const graceSummary = { id: '8', name: 'Grace', avatar: null }
const adaSummary = { id: '7', name: 'Ada', avatar: null }

/** A rule as the server sends it: every field is there, and null when the kind doesn't use it. */
const fullRule = (r: Partial<Occasion['rule']> & Pick<Occasion['rule'], 'kind' | 'month'>): Occasion['rule'] => ({
  year: null,
  day: null,
  weekday: null,
  n: null,
  ...r,
})

const birthday = (id: string, user: typeof adaSummary, next: string): Occasion => ({
  id,
  kind: 'birthday',
  name: 'Birthday',
  rule: fullRule({ kind: 'annual', month: 12, day: 10 }),
  user,
  is_everyone: null,
  next_date: next,
  can_edit: false,
  can_delete: false,
  can_attach_group: false,
})
const custom = (id: string, name: string, rule: Occasion['rule'], next: string | null, more: Partial<Occasion> = {}): Occasion => ({
  id,
  kind: 'custom',
  name,
  rule,
  user: adaSummary,
  is_everyone: null,
  next_date: next,
  can_edit: true,
  can_delete: true,
  can_attach_group: false,
  ...more,
})

const adaBirthday = birthday('1', adaSummary, '2026-12-10')
const wedding = custom('2', 'Wedding day', fullRule({ kind: 'annual', month: 5, day: 4 }), '2027-05-04')
const advent = custom('3', 'First Advent', fullRule({ kind: 'nth_weekday', n: 1, weekday: 0, month: 12 }), '2026-12-06')
const lastMonday = custom('4', 'Fete', fullRule({ kind: 'last_weekday', weekday: 1, month: 6 }), '2027-06-28')
const gone = custom('5', 'Old news', fullRule({ kind: 'once', year: 2020, month: 5, day: 5 }), null)

const asAda = { [ME]: { status: 200, body: { ...ada, can_add_occasion: true } } }
const adaUser: User = { ...ada, can_add_occasion: true }
const adasOccasions = page([advent, adaBirthday, wedding, lastMonday, gone])

describe('the occasions on the profile screen', () => {
  const onProfile = (more = {}) => ({
    ...asAda,
    'GET /api/v2/users/7': { status: 200, body: adaUser },
    'GET /api/v2/occasions?user_id=7': adasOccasions,
    ...more,
  })

  test('lists the birthday and the custom occasions in the order the server gives, with Edit on the custom ones', async () => {
    serve(onProfile())
    open('/users/7/profile')

    await heading('Your profile')
    const list = await screen.findByRole('list', { name: 'Occasions' })
    const rows = within(list).getAllByRole('listitem')
    expect(rows.map((li) => li.textContent)).toEqual([
      'Dec 6, 2026First Advent1st Sunday of December, every yearEdit First Advent',
      'Dec 10, 2026BirthdayFrom the birthday above',
      'May 4, 2027Wedding dayEvery year on May 4Edit Wedding day',
      'Jun 28, 2027FeteLast Monday of June, every yearEdit Fete',
      'May 5, 2020Old newsAlready passedEdit Old news',
    ])
    expect(within(rows[0]!).getByRole('link', { name: 'Edit First Advent' })).toHaveAttribute('href', '/users/7/occasions/3')
    expect(within(rows[1]!).queryByRole('link')).not.toBeInTheDocument()
  })

  test('shows Add an occasion from can_add_occasion, and opens the add screen', async () => {
    serve(onProfile())
    open('/users/7/profile')

    expect(await screen.findByRole('link', { name: 'Add an occasion' })).toHaveAttribute('href', '/users/7/occasions/new')
  })

  test('has no Add for a viewer who cannot add, such as the admin on another user, and shows only the occasions the server gives', async () => {
    serve({
      ...asAda,
      'GET /api/v2/users/8': { status: 200, body: { ...grace, can_add_occasion: false } },
      'GET /api/v2/occasions?user_id=8': page([birthday('9', graceSummary, '2026-12-09')]),
    })
    open('/users/8/profile')

    await heading("Grace's profile")
    const list = await screen.findByRole('list', { name: 'Occasions' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(1)
    expect(screen.queryByRole('link', { name: 'Add an occasion' })).not.toBeInTheDocument()
  })

  test('has no Edit on an occasion the viewer cannot change', async () => {
    serve(onProfile({ 'GET /api/v2/occasions?user_id=7': page([{ ...wedding, can_edit: false, can_delete: false }]) }))
    open('/users/7/profile')

    const list = await screen.findByRole('list', { name: 'Occasions' })
    expect(within(list).queryByRole('link', { name: /Edit/ })).not.toBeInTheDocument()
  })

  test('says so when the occasions cannot load, and leaves the form', async () => {
    serve(onProfile({ 'GET /api/v2/occasions?user_id=7': problem(500, 'internal_error') }))
    open('/users/7/profile')

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load occasions. Reload and try again.')
    expect(screen.getByLabelText('First name')).toBeInTheDocument()
  })
})

describe('Add an occasion', () => {
  const onAdd = (more = {}) => ({
    ...asAda,
    'GET /api/v2/users/7': { status: 200, body: adaUser },
    ...more,
  })

  test('sends a same-date-every-year occasion for the user, and goes back to the profile', async () => {
    const user = userEvent.setup()
    const fetch = serve(
      onAdd({
        'POST /api/v2/occasions': { status: 201, body: wedding },
        'GET /api/v2/occasions?user_id=7': adasOccasions,
      }),
    )
    open('/users/7/occasions/new')

    expect(await heading('Add an occasion')).toBeInTheDocument()
    await user.type(screen.getByLabelText("What's it called?"), '  Wedding day ')
    await user.selectOptions(screen.getByLabelText('Month'), 'May')
    await user.type(screen.getByLabelText('Day'), '4')
    await user.click(screen.getByRole('button', { name: 'Add this occasion' }))

    expect(await heading('Your profile')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/occasions')).toEqual({
      kind: 'custom',
      name: 'Wedding day',
      user_id: '7',
      rule: { kind: 'annual', month: 5, day: 4 },
    })
    expect(window.location.pathname).toBe('/users/7/profile')
  })

  test.each([
    ['one time only', 'One time only', async (user: ReturnType<typeof userEvent.setup>) => {
      await user.type(screen.getByLabelText('Date'), '2027-05-14')
    }, { kind: 'once', year: 2027, month: 5, day: 14 }],
    ['a set weekday every year', 'A set weekday every year', async (user: ReturnType<typeof userEvent.setup>) => {
      await user.selectOptions(screen.getByLabelText('Which'), '2nd')
      await user.selectOptions(screen.getByLabelText('Day'), 'Sunday')
      await user.selectOptions(screen.getByLabelText('Month'), 'May')
    }, { kind: 'nth_weekday', n: 2, weekday: 0, month: 5 }],
    ['the last weekday of a month', 'Last weekday of a month', async (user: ReturnType<typeof userEvent.setup>) => {
      await user.selectOptions(screen.getByLabelText('Day'), 'Monday')
      await user.selectOptions(screen.getByLabelText('Month'), 'May')
    }, { kind: 'last_weekday', weekday: 1, month: 5 }],
  ])('sends %s', async (_name, choice, fill, rule) => {
    const user = userEvent.setup()
    const fetch = serve(onAdd({ 'POST /api/v2/occasions': { status: 201, body: wedding }, 'GET /api/v2/occasions?user_id=7': adasOccasions }))
    open('/users/7/occasions/new')

    await heading('Add an occasion')
    await user.type(screen.getByLabelText("What's it called?"), 'Mother’s Day')
    await user.click(screen.getByRole('button', { name: new RegExp(choice) }))
    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Add this occasion' }))

    await heading('Your profile')
    expect(sentBody(fetch, 'POST /api/v2/occasions')).toEqual({ kind: 'custom', name: 'Mother’s Day', user_id: '7', rule })
  })

  test('names the child in the heading and sends their ID', async () => {
    const user = userEvent.setup()
    const emma: User = { ...grace, id: '9', name: 'Emma', can_add_occasion: true }
    const fetch = serve({
      ...asAda,
      'GET /api/v2/users/9': { status: 200, body: emma },
      'POST /api/v2/occasions': { status: 201, body: wedding },
      'GET /api/v2/occasions?user_id=9': page([]),
    })
    open('/users/9/occasions/new')

    expect(await heading('Add an occasion for Emma')).toBeInTheDocument()
    await user.type(screen.getByLabelText("What's it called?"), 'Recital')
    await user.selectOptions(screen.getByLabelText('Month'), 'March')
    await user.type(screen.getByLabelText('Day'), '3')
    await user.click(screen.getByRole('button', { name: 'Add this occasion' }))

    await heading("Emma's profile")
    expect(sentBody(fetch, 'POST /api/v2/occasions')).toMatchObject({ user_id: '9' })
  })

  test('needs a name, a month, and a day, and shows the words of a refusal', async () => {
    const user = userEvent.setup()
    serve(onAdd({ 'POST /api/v2/occasions': problem(422, 'invalid_input', 'That date is not in the calendar.') }))
    open('/users/7/occasions/new')

    await heading('Add an occasion')
    await user.click(screen.getByRole('button', { name: 'Add this occasion' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter a name for the occasion.')
    await user.type(screen.getByLabelText("What's it called?"), 'Odd day')
    await user.click(screen.getByRole('button', { name: 'Add this occasion' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Choose a month.'))
    await user.selectOptions(screen.getByLabelText('Month'), 'February')
    await user.click(screen.getByRole('button', { name: 'Add this occasion' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Enter the day of the month.'))
    await user.type(screen.getByLabelText('Day'), '30')
    await user.click(screen.getByRole('button', { name: 'Add this occasion' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('That date is not in the calendar.'))
    expect(window.location.pathname).toBe('/users/7/occasions/new')
  })

  test('is the missing-page message for a user the viewer cannot add for', async () => {
    serve({ ...asAda, 'GET /api/v2/users/8': { status: 200, body: { ...grace, can_add_occasion: false } } })
    open('/users/8/occasions/new')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('Edit an occasion', () => {
  const onEdit = (o: Occasion, more = {}) => ({
    ...asAda,
    [`GET /api/v2/occasions/${o.id}`]: { status: 200, body: o },
    ...more,
  })

  test('is filled in with the occasion, saves the name and the rule, and goes back to the profile', async () => {
    const user = userEvent.setup()
    const fetch = serve(
      onEdit(advent, {
        'PATCH /api/v2/occasions/3': { status: 200, body: advent },
        'GET /api/v2/users/7': { status: 200, body: adaUser },
        'GET /api/v2/occasions?user_id=7': adasOccasions,
      }),
    )
    open('/users/7/occasions/3')

    expect(await heading('Edit an occasion')).toBeInTheDocument()
    expect(screen.getByLabelText("What's it called?")).toHaveValue('First Advent')
    expect(screen.getByRole('button', { name: /A set weekday every year/ })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText('Which')).toHaveValue('1')
    expect(screen.getByLabelText('Day')).toHaveValue('0')
    expect(screen.getByLabelText('Month')).toHaveValue('12')
    await user.clear(screen.getByLabelText("What's it called?"))
    await user.type(screen.getByLabelText("What's it called?"), 'Advent Sunday')
    await user.click(screen.getByRole('button', { name: 'Save this occasion' }))

    expect(await heading('Your profile')).toBeInTheDocument()
    expect(sentBody(fetch, 'PATCH /api/v2/occasions/3')).toEqual({
      name: 'Advent Sunday',
      rule: { kind: 'nth_weekday', n: 1, weekday: 0, month: 12 },
    })
    const call = fetch.mock.calls.find(([, init]) => init?.method === 'PATCH')!
    expect(new Headers(call[1]?.headers).get('Content-Type')).toBe('application/merge-patch+json')
  })

  test('has a link to delete the occasion, from can_delete', async () => {
    serve(onEdit(wedding))
    open('/users/7/occasions/2')

    expect(await screen.findByRole('link', { name: 'Delete this occasion' })).toHaveAttribute('href', '/users/7/occasions/2/delete')
  })

  test('has no link to delete an occasion that can be changed but not deleted', async () => {
    serve(onEdit({ ...wedding, can_delete: false }))
    open('/users/7/occasions/2')

    await heading('Edit an occasion')
    expect(screen.queryByRole('link', { name: 'Delete this occasion' })).not.toBeInTheDocument()
  })

  test('shows a past one-time date in the date field', async () => {
    serve(onEdit(gone))
    open('/users/7/occasions/5')

    await heading('Edit an occasion')
    expect(screen.getByLabelText('Date')).toHaveValue('2020-05-05')
  })

  test.each([
    ['a birthday', adaBirthday, '/users/7/occasions/1'],
    ['an occasion the viewer cannot change', { ...wedding, can_edit: false, can_delete: false }, '/users/7/occasions/2'],
    ['an occasion of another user', { ...wedding, user: graceSummary }, '/users/7/occasions/2'],
  ])('is the missing-page message for %s', async (_name, o, path) => {
    serve(onEdit(o))
    open(path)

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })

  test('is the missing-page message for an occasion the server does not show', async () => {
    serve({ ...asAda, 'GET /api/v2/occasions/9': problem(404, 'not_found') })
    open('/users/7/occasions/9')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('Delete an occasion', () => {
  test('asks first, then deletes and goes to the profile', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'GET /api/v2/occasions/2': { status: 200, body: wedding },
      'DELETE /api/v2/occasions/2': { status: 204 },
      'GET /api/v2/users/7': { status: 200, body: adaUser },
      'GET /api/v2/occasions?user_id=7': page([adaBirthday]),
    })
    open('/users/7/occasions/2/delete')

    expect(await heading('Delete this occasion?')).toBeInTheDocument()
    expect(screen.getByText('Wedding day goes.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Keep it' })).toHaveAttribute('href', '/users/7/occasions/2')
    await user.click(screen.getByRole('button', { name: 'Delete this occasion' }))

    expect(await heading('Your profile')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/occasions/2', expect.objectContaining({ method: 'DELETE' }))
    expect(window.location.pathname).toBe('/users/7/profile')
  })

  test('is the missing-page message for an occasion the viewer cannot delete', async () => {
    serve({ ...asAda, 'GET /api/v2/occasions/2': { status: 200, body: { ...wedding, can_delete: false } } })
    open('/users/7/occasions/2/delete')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})
