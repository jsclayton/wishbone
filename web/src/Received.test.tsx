import { screen, within } from '@testing-library/react'
import type { Gift, ReceivedGift } from './api'
import { aGift, ada, heading, ME, open, page, problem, serve } from './test/fake'

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const asAda = { [ME]: { status: 200, body: ada } }
const RECEIVED = 'GET /api/v2/gifts?recipient_id=me&sort=-given_at'
const GIVEN = 'GET /api/v2/gifts?giver_id=me&state=given&sort=-given_at'

const sue = { id: '5', name: 'Sue', avatar: null }
/** What a recipient is sent: no note, bought mark, flags, needs_attention, or created_at. */
const received = (over: Partial<ReceivedGift> = {}): ReceivedGift => {
  const { is_bought: _b, note: _n, flags: _f, needs_attention: _a, created_at: _c, ...shared } = aGift
  return {
    ...shared,
    id: '70',
    state: 'given',
    given_at: '2026-12-07T00:00:00Z',
    title: 'A grill',
    giver: sue,
    giver_name: 'Sue',
    can_edit: false,
    can_delete: false,
    can_raise_quantity: false,
    can_acknowledge: false,
    recipients: [{ id: ada.id, name: 'Ada', avatar: null }],
    ...over,
  }
}

describe('Received', () => {
  test('lists the gifts given to the user, by occasion, with who gave each one', async () => {
    const advent = received({ id: '70', title: 'A grill', occasion_name: 'First Advent', occasion_date: '2026-12-06' })
    const mug = received({ id: '71', title: 'A mug', quantity: 2, occasion_name: 'First Advent', occasion_date: '2026-12-06', giver: null, giver_name: 'Tess' })
    const birthday = received({ id: '72', title: 'Socks', occasion_name: 'Birthday', occasion_date: '2026-05-06' })
    serve({ ...asAda, [RECEIVED]: page([advent, mug, birthday]) })
    open('/received')

    expect(await heading('Received')).toBeInTheDocument()
    const groups = await screen.findAllByRole('region')
    expect(groups.map((g) => g.getAttribute('aria-label'))).toEqual(['First Advent, Dec 6, 2026', 'Your birthday, May 6, 2026'])
    expect(within(groups[0]!).getAllByRole('heading', { level: 3 }).map((h) => h.textContent)).toEqual(['A grill', 'A mug (2)'])
    expect(within(groups[0]!).getByText('From Sue')).toBeInTheDocument()
    expect(within(groups[0]!).getByText('From Tess')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /I didn't get this|Add again/ }), 'the server says nobody can').not.toBeInTheDocument()
  })

  test('offers "I didn\'t get this" and Add again on a gift that can be restored', async () => {
    serve({ ...asAda, [RECEIVED]: page([received({ can_restore: true, can_add_again: true })]) })
    open('/received')

    expect(await screen.findByRole('link', { name: "I didn't get this: A grill" })).toHaveAttribute('href', '/gifts/70/restore')
    expect(screen.getByRole('link', { name: 'Add again: A grill' })).toHaveAttribute('href', '/gifts/70/add-again')
  })

  test('says "From {label} · claimed by {giver}" for a gift with a from label', async () => {
    serve({ ...asAda, [RECEIVED]: page([received({ from_label: 'Emma' })]) })
    open('/received')

    expect(await screen.findByText('From Emma · claimed by Sue')).toBeInTheDocument()
    expect(screen.queryByText('From Sue')).not.toBeInTheDocument()
  })

  test("shows another user's birthday under its own name", async () => {
    const dad = { id: '10', name: 'Dad', avatar: null }
    serve({ ...asAda, [RECEIVED]: page([received({ occasion_name: 'Birthday', recipients: [dad, { id: ada.id, name: 'Ada', avatar: null }] })]) })
    open('/received')

    expect(await screen.findByRole('region', { name: 'Birthday, Dec 6, 2026' })).toBeInTheDocument()
  })

  test('says so when nothing was given yet', async () => {
    serve({ ...asAda, [RECEIVED]: page([]) })
    open('/received')

    expect(await screen.findByRole('heading', { level: 2, name: 'Nothing here yet' })).toBeInTheDocument()
  })

  test('says so when it cannot load', async () => {
    serve({ ...asAda, [RECEIVED]: problem(500, 'internal_error') })
    open('/received')

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load what you received')
  })
})

describe('Given', () => {
  const given = (over: Partial<Gift> = {}): Gift => ({
    ...aGift,
    id: '80',
    state: 'given',
    given_at: '2026-12-07T00:00:00Z',
    title: 'A grill',
    occasion_name: 'First Advent',
    can_edit: false,
    can_delete: false,
    ...over,
  })

  test('lists the gifts the user gave, by occasion, with who they went to', async () => {
    const grill = given({ id: '80', recipients: [{ id: '9', name: 'Mom', avatar: null }, { id: '10', name: 'Dad', avatar: null }], from_label: 'Emma', note: 'Costco' })
    const hidden = given({ id: '81', title: 'A rug', occasion_name: null, occasion: null, occasion_date: '2026-12-06', recipients: undefined })
    serve({ ...asAda, [GIVEN]: page([grill, hidden]) })
    open('/given')

    expect(await heading('Given')).toBeInTheDocument()
    const groups = await screen.findAllByRole('region')
    expect(groups.map((g) => g.getAttribute('aria-label'))).toEqual(['First Advent, Dec 6, 2026', 'Occasion, Dec 6, 2026'])
    expect(within(groups[0]!).getByText('To Mom and Dad')).toBeInTheDocument()
    expect(within(groups[0]!).getByText('From Emma')).toBeInTheDocument()
    expect(within(groups[0]!).getByText('Costco')).toBeInTheDocument()
    expect(within(groups[1]!).getByText('To Mom')).toBeInTheDocument()
  })

  test('says so when the user gave nothing yet', async () => {
    serve({ ...asAda, [GIVEN]: page([]) })
    open('/given')

    expect(await screen.findByRole('heading', { level: 2, name: 'Nothing yet' })).toBeInTheDocument()
  })

  test('is reached from Menu, with Received', async () => {
    serve({ ...asAda })
    open('/menu')

    expect(await screen.findByRole('link', { name: 'Received' })).toHaveAttribute('href', '/received')
    expect(screen.getByRole('link', { name: 'Given' })).toHaveAttribute('href', '/given')
  })
})
