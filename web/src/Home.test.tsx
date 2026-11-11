import { screen, waitFor, within } from '@testing-library/react'
import type { Occasion } from './api'
import { ada, grace, heading, ME, open, page, problem, serve } from './test/fake'

// Today is Nov 8 2026, so the window ends on Jan 7 2027.
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

const rule = { kind: 'annual' as const, month: 1, year: null, day: 1, weekday: null, n: null }
const theo = { id: '9', name: 'Theo', avatar: null }
const adaSummary = { id: '7', name: 'Ada', avatar: null }

const occasion = (id: string, name: string, kind: Occasion['kind'], next: string, more: Partial<Occasion> = {}): Occasion => ({
  id,
  kind,
  name,
  rule,
  user: kind === 'shared' ? null : theo,
  is_everyone: kind === 'shared' ? true : null,
  next_date: next,
  can_edit: false,
  can_delete: false,
  can_attach_group: false,
  ...more,
})

const adasBirthday = occasion('1', 'Birthday', 'birthday', '2026-11-20', { user: adaSummary })
const theosBirthday = occasion('2', 'Birthday', 'birthday', '2026-12-06')
const christmas = occasion('3', 'Christmas', 'shared', '2026-12-25')
const newYear = occasion('4', 'New Year', 'shared', '2027-01-01')

const home = (occasions: ReturnType<typeof page> | ReturnType<typeof problem>) => ({
  [ME]: { status: 200, body: ada },
  'GET /api/v2/groups?includes_me=true': page([]),
  'GET /api/v2/invitations?user_id=7&limit=1': page([]),
  'GET /api/v2/occasions?next_date_before=2027-01-07': occasions,
})

describe("Home's band", () => {
  test("shows the next occasion that isn't the user's own, with its countdown and a link to that user's page", async () => {
    serve(home(page([adasBirthday, theosBirthday, christmas])))
    open('/')

    await heading('Home')
    const band = await screen.findByText(/Next up/)
    expect(band).toHaveTextContent('Next up, Sun Dec 6')
    expect(screen.getByText('28 days')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: "See Theo's list" })).toHaveAttribute('href', '/users/9')
    // The band doesn't link to the shared occasion that is later, and it doesn't speak of Ada's own birthday.
    expect(screen.queryByRole('link', { name: /See who's on/ })).not.toBeInTheDocument()
  })

  test('shows "See who\'s on it" for a shared occasion, which opens it', async () => {
    serve(home(page([adasBirthday, christmas, newYear])))
    open('/')

    await heading('Home')
    expect(await screen.findByText(/Next up/)).toHaveTextContent('Next up, Fri Dec 25')
    expect(screen.getByText('47 days')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: "See who's on it" })).toHaveAttribute('href', '/occasions/3')
  })

  test('says "Today" on the day, and names each occasion when several fall on it', async () => {
    const today = occasion('5', "Theo's day", 'custom', '2026-11-08')
    const sharedToday = occasion('6', 'Founders Day', 'shared', '2026-11-08')
    serve(home(page([today, sharedToday, christmas])))
    open('/')

    await heading('Home')
    // The band says Today in big words, and Coming up says it again on the row.
    expect(await screen.findAllByText('Today')).toHaveLength(3)
    expect(screen.getByRole('link', { name: "See Theo's list" })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: "See who's on Founders Day" })).toHaveAttribute('href', '/occasions/6')
  })

  test('says nothing is coming up when the window is empty or holds only the user\'s own occasions', async () => {
    serve(home(page([adasBirthday])))
    open('/')

    await heading('Home')
    expect(await screen.findByText('Nothing coming up')).toBeInTheDocument()
    expect(screen.getByText('No birthdays or occasions.')).toBeInTheDocument()
  })

  test('is quiet while the occasions are loading or when they fail', async () => {
    serve(home(problem(500, 'internal')))
    open('/')

    await heading('Home')
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load upcoming occasions')
    expect(screen.queryByText(/Next up/)).not.toBeInTheDocument()
    expect(screen.queryByText('Nothing coming up')).not.toBeInTheDocument()
  })
})

describe("Home's band, turning N", () => {
  // Theo's birthday is Dec 6 2026, and he was born in 2019.
  const theo2019 = { ...grace, id: '9', name: 'Theo', birthday: { month: 12, day: 6, year: 2019 } }

  test("says how old a birthday's user is turning, from the year of their birthday", async () => {
    serve({ ...home(page([theosBirthday])), 'GET /api/v2/users/9': { status: 200, body: theo2019 } })
    open('/')

    await heading('Home')
    expect(await screen.findByText("Theo's birthday, turning 7")).toBeInTheDocument()
  })

  test('says nothing of age when the birthday has no year', async () => {
    const fetch = serve({ ...home(page([theosBirthday])), 'GET /api/v2/users/9': { status: 200, body: { ...theo2019, birthday: { month: 12, day: 6, year: null } } } })
    open('/')

    await heading('Home')
    // The band and Coming up both say it.
    expect(await screen.findAllByText("Theo's birthday")).toHaveLength(2)
    await waitFor(() => expect(fetch.mock.calls.map(([url]) => String(url))).toContain('/api/v2/users/9'))
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(screen.queryByText(/turning/)).not.toBeInTheDocument()
  })

  test('asks for the user only for a birthday in the band', async () => {
    const fetch = serve(home(page([christmas, theosBirthday])))
    open('/')

    await heading('Home')
    await screen.findByText(/Next up/)
    expect(fetch.mock.calls.map(([url]) => String(url))).not.toContain('/api/v2/users/9')
  })
})

describe('Coming up', () => {
  test('lists every occasion in the window, soonest first, each opening its user or the shared occasion', async () => {
    serve(home(page([adasBirthday, theosBirthday, christmas, newYear])))
    open('/')

    const section = (await screen.findByRole('heading', { name: 'Coming up' })).closest('section')!
    const rows = within(section).getAllByRole('listitem')
    expect(rows.map((li) => li.textContent)).toEqual(['Nov 20Your birthday12d', "Dec 6Theo's birthday28d", 'Dec 25Christmas47d', 'Jan 1New Year54d'])
    expect(within(rows[0]!).getByRole('link')).toHaveAttribute('href', '/users/7')
    expect(within(rows[1]!).getByRole('link')).toHaveAttribute('href', '/users/9')
    expect(within(rows[2]!).getByRole('link')).toHaveAttribute('href', '/occasions/3')
  })

  test('is left out when nothing is coming up', async () => {
    serve(home(page([])))
    open('/')

    await heading('Home')
    expect(await screen.findByText('Nothing coming up')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Coming up' })).not.toBeInTheDocument()
  })
})
