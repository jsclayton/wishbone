import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Item, List, Occasion } from './api'
import { ada, adaList, heading, kettle, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

// Ada is a giver on Mom's list. The kettle has 3 wanted, and nobody has claimed any.
const mom = { id: '9', name: 'Mom', avatar: null }
const momsList: List = { ...adaList, id: '12', name: 'Mom', is_primary: true, recipients: [mom], can_edit: false, can_delete: false }
const wanted: Item = { ...kettle, list_id: '12', quantity: 3, still_wanted: 3, my_claimed_quantity: 0, can_edit: false, can_delete: false, can_claim: true }
const occasion = (id: string, name: string, next: string): Occasion => ({
  id,
  kind: name === 'Birthday' ? 'birthday' : 'custom',
  name,
  rule: { kind: 'annual', month: 12, day: 6, year: null, weekday: null, n: null },
  user: mom,
  is_everyone: null,
  next_date: next,
  can_edit: false,
  can_delete: false,
  can_attach_group: false,
})
const advent = occasion('31', 'First Advent', '2026-12-06')
const birthday = occasion('32', 'Birthday', '2027-01-01')

const claiming = (item: Item = wanted) => ({
  [ME]: { status: 200, body: ada },
  [`GET /api/v2/items/${item.id}`]: { status: 200, body: item },
  [`GET /api/v2/lists/${item.list_id}`]: { status: 200, body: momsList },
  [`GET /api/v2/occasions?list_id=${item.list_id}`]: page([advent, birthday]),
  'GET /api/v2/users': page([ada]),
})

const claimed = { status: 201, body: { id: '60', quantity: 1 } }

describe("I'll get this", () => {
  test('shows the item and its occasions, with the next one chosen, and claims with it', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...claiming(), 'POST /api/v2/items/21/gifts': claimed })
    open('/items/21/claim')

    expect(await heading("I'll get this")).toBeInTheDocument()
    expect(await screen.findByText('A kettle')).toBeInTheDocument()
    expect(await screen.findByText('For Mom')).toBeInTheDocument()
    const picker = await screen.findByRole('group', { name: 'Which occasion?' })
    expect(picker).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /First Advent/ })).toBeChecked()
    expect(screen.getByRole('radio', { name: /birthday/ })).not.toBeChecked()
    expect(screen.getByText("Mom won't know. Nobody else will either.")).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: "I'll get this" }))

    await waitFor(() => expect(window.location.pathname).toBe('/items/21'))
    expect(sentBody(fetch, 'POST /api/v2/items/21/gifts')).toEqual({ quantity: 1, occasion_id: '31' })
  })

  test('claims another occasion and how many, up to what is still wanted', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...claiming({ ...wanted, still_wanted: 2 }), 'POST /api/v2/items/21/gifts': claimed })
    open('/items/21/claim')

    await user.click(await screen.findByRole('radio', { name: /birthday/ }))
    await user.click(screen.getByRole('button', { name: 'One more' }))
    await user.click(screen.getByRole('button', { name: 'One more' }))
    expect(screen.getByText('2')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: "I'll get this" }))

    await waitFor(() => expect(sentBody(fetch, 'POST /api/v2/items/21/gifts')).toEqual({ quantity: 2, occasion_id: '32' }))
  })

  test('asks no count for a single one', async () => {
    serve(claiming({ ...wanted, quantity: 1, still_wanted: 1 }))
    open('/items/21/claim')

    await screen.findByRole('group', { name: 'Which occasion?' })
    expect(screen.queryByText('How many')).not.toBeInTheDocument()
  })

  test('says that someone else got it, without saying who, when the last one went first', async () => {
    const user = userEvent.setup()
    serve({
      ...claiming(),
      'POST /api/v2/items/21/gifts': problem(409, 'item_fully_claimed', 'Someone else just claimed some of this.'),
    })
    open('/items/21/claim')

    await user.click(await screen.findByRole('button', { name: "I'll get this" }))

    expect(await heading('Someone else just got this')).toBeInTheDocument()
    expect(screen.getByText("A kettle is no longer on Mom's list, so it wasn't added to Giving.")).toBeInTheDocument()
    expect(screen.getByRole('link', { name: "Back to Mom's list" })).toHaveAttribute('href', '/lists/12')
  })

  test('points to Giving when the gift for that occasion exists already', async () => {
    const user = userEvent.setup()
    serve({ ...claiming(), 'POST /api/v2/items/21/gifts': problem(409, 'already_claimed', 'You already claimed this.') })
    open('/items/21/claim')

    await user.click(await screen.findByRole('button', { name: "I'll get this" }))

    expect(await screen.findByRole('alert')).toHaveTextContent("You're already getting this for Mom's First Advent. Choose another occasion.")
    expect(screen.getByRole('link', { name: 'Giving' })).toHaveAttribute('href', '/giving')
    expect(window.location.pathname).toBe('/items/21/claim')
  })

  test('shows the server\'s words for any other refusal, and lets the user try again', async () => {
    const user = userEvent.setup()
    serve({ ...claiming(), 'POST /api/v2/items/21/gifts': problem(403, 'recipient_cannot_claim', "You can't claim here.") })
    open('/items/21/claim')

    await user.click(await screen.findByRole('button', { name: "I'll get this" }))

    expect(await screen.findByRole('alert')).toHaveTextContent("You can't claim here.")
    expect(screen.getByRole('button', { name: "I'll get this" })).toBeEnabled()
  })

  test('says that nothing is left when the item cannot be claimed any more', async () => {
    serve(claiming({ ...wanted, can_claim: false, still_wanted: 0 }))
    open('/items/21/claim')

    expect(await heading('Someone else just got this')).toBeInTheDocument()
  })

  test('says so, and points to Giving, when the giver\'s own gifts took the last ones', async () => {
    serve(claiming({ ...wanted, can_claim: false, still_wanted: 0, my_claimed_quantity: 3 }))
    open('/items/21/claim')

    expect(await heading('You have all of this')).toBeInTheDocument()
    expect(screen.queryByText('Someone else just got this')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Giving' })).toHaveAttribute('href', '/giving')
  })

  test('says it cannot be claimed when some are wanted but the giver cannot give, and shows no claim state to a recipient', async () => {
    serve(claiming({ ...wanted, can_claim: false }))
    open('/items/21/claim')
    expect(await heading("You can't get this")).toBeInTheDocument()
  })

  test('is the missing page for a recipient, whose item has no claim state', async () => {
    const { still_wanted: _a, my_claimed_quantity: _b, ...recipient } = wanted
    serve(claiming({ ...recipient, can_claim: false }))
    open('/items/21/claim')
    expect(await heading("We can't find that page")).toBeInTheDocument()
  })

  test('shows the missing-page message for an item the user cannot see', async () => {
    serve({ [ME]: { status: 200, body: ada }, 'GET /api/v2/items/21': problem(404, 'not_found') })
    open('/items/21/claim')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})
