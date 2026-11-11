import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { List, ReceivedGift, User } from './api'
import { addAgainList } from './GiftAgain'
import { aGift, ada, adaList, heading, ME, open, page, problem, sentBody, serve } from './test/fake'

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const asAda = { [ME]: { status: 200, body: ada } }
const sue = { id: '5', name: 'Sue', avatar: null }
const emma = { id: '20', name: 'Emma', avatar: null }
const zoe = { id: '21', name: 'Zoe', avatar: null }

/** A given gift as a recipient sees it, with the buttons the server allows. */
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
    can_restore: true,
    can_add_again: true,
    recipients: [{ id: ada.id, name: 'Ada', avatar: null }],
    ...over,
  }
}

const aList = (over: Partial<List>): List => ({ ...adaList, ...over })
const kids: User = { ...ada, children: [emma, zoe] }

describe('the screen before I didn\'t get this', () => {
  test('says what happens, restores on the button, and returns to Received', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...asAda, 'GET /api/v2/gifts/70': { status: 200, body: received() }, 'POST /api/v2/gifts/70/restore': { status: 204 } })
    open('/gifts/70/restore')

    expect(await heading("Didn't get this?")).toBeInTheDocument()
    expect(screen.getByText('A grill leaves Received.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Keep it' })).toHaveAttribute('href', '/received')
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/gifts/70/restore', expect.anything())
    await user.click(screen.getByRole('button', { name: "I didn't get this" }))

    await waitFor(() => expect(window.location.pathname).toBe('/received'))
    expect(fetch).toHaveBeenCalledWith('/api/v2/gifts/70/restore', expect.objectContaining({ method: 'POST' }))
  })

  test('stays and says so when the restore fails', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, 'GET /api/v2/gifts/70': { status: 200, body: received() }, 'POST /api/v2/gifts/70/restore': problem(500, 'internal_error') })
    open('/gifts/70/restore')

    await user.click(await screen.findByRole('button', { name: "I didn't get this" }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong')
    expect(window.location.pathname).toBe('/gifts/70/restore')
  })

  test('shows the missing-page message for a gift that is gone or one the user cannot restore', async () => {
    serve({ ...asAda, 'GET /api/v2/gifts/70': { status: 200, body: received({ can_restore: false }) }, 'GET /api/v2/gifts/71': problem(404, 'not_found') })
    open('/gifts/70/restore')
    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('the list that Add again starts on', () => {
  const mine = aList({ id: '11', name: 'Ada', is_primary: true })
  const extra = aList({ id: '12', name: 'Garden', is_primary: false })
  const emmas = aList({ id: '30', name: 'Emma', is_primary: true, recipients: [emma] })
  const zoes = aList({ id: '31', name: 'Zoe', is_primary: true, recipients: [zoe] })
  const gift = (over: Partial<ReceivedGift> = {}) => received(over)

  test("is the gift's own list while the user can still edit it", () => {
    expect(addAgainList(gift({ list: { id: '12', name: 'Garden' } }), [mine, extra], ada)).toBe('12')
  })

  test("is the user's own primary list when the list is gone and the user is a recipient", () => {
    expect(addAgainList(gift({ list: { id: '99', name: 'Gone' } }), [extra, mine], ada)).toBe('11')
  })

  test("is the primary list of the first child by name among the gift's recipients when the user is not one", () => {
    const g = gift({ list: { id: '99', name: 'Gone' }, recipients: [zoe, emma] })
    expect(addAgainList(g, [mine, zoes, emmas], kids)).toBe('30')
    expect(addAgainList(gift({ list: { id: '99', name: 'Gone' }, recipients: [zoe] }), [mine, zoes, emmas], kids)).toBe('31')
  })

  test('is nothing when none of them is a list the user can edit', () => {
    expect(addAgainList(gift({ list: { id: '99', name: 'Gone' }, recipients: [sue] }), [mine], ada)).toBeUndefined()
  })
})

describe('Add again', () => {
  test('adds the gift to the only list the user can edit, and opens it', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'GET /api/v2/gifts/70': { status: 200, body: received() },
      'GET /api/v2/lists?can_edit=true': page([adaList]),
      'POST /api/v2/lists/11/items': { status: 201, body: { id: '90', title: 'A grill' } },
    })
    open('/gifts/70/add-again')

    expect(await heading('Add again')).toBeInTheDocument()
    expect(screen.getByText('A grill')).toBeInTheDocument()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Add again' }))

    await waitFor(() => expect(window.location.pathname).toBe('/lists/11'))
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toEqual({ from_gift_id: '70' })
  })

  test("starts on the gift's list and adds to the list the user picks", async () => {
    const user = userEvent.setup()
    const garden = aList({ id: '12', name: 'Garden', is_primary: false })
    const fetch = serve({
      ...asAda,
      'GET /api/v2/gifts/70': { status: 200, body: received({ list: { id: '12', name: 'Garden' } }) },
      'GET /api/v2/lists?can_edit=true': page([adaList, garden]),
      'POST /api/v2/lists/11/items': { status: 201, body: { id: '90', title: 'A grill' } },
    })
    open('/gifts/70/add-again')

    const choice = await screen.findByRole('combobox', { name: 'List' })
    expect(choice).toHaveValue('12')
    await user.selectOptions(choice, '11')
    await user.click(screen.getByRole('button', { name: 'Add again' }))

    await waitFor(() => expect(window.location.pathname).toBe('/lists/11'))
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toEqual({ from_gift_id: '70' })
  })

  test("starts on the user's own primary list when the gift's list is gone", async () => {
    const garden = aList({ id: '12', name: 'Garden', is_primary: false })
    serve({
      ...asAda,
      'GET /api/v2/gifts/70': { status: 200, body: received({ list: { id: '99', name: 'Gone' } }) },
      'GET /api/v2/lists?can_edit=true': page([garden, adaList]),
    })
    open('/gifts/70/add-again')

    expect(await screen.findByRole('combobox', { name: 'List' })).toHaveValue('11')
  })

  test('says what the server refused and stays', async () => {
    const user = userEvent.setup()
    serve({
      ...asAda,
      'GET /api/v2/gifts/70': { status: 200, body: received() },
      'GET /api/v2/lists?can_edit=true': page([adaList]),
      'POST /api/v2/lists/11/items': problem(422, 'reference_not_found', 'That gift is gone.'),
    })
    open('/gifts/70/add-again')

    await user.click(await screen.findByRole('button', { name: 'Add again' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('That gift is gone.')
    expect(window.location.pathname).toBe('/gifts/70/add-again')
  })

  test('shows the missing-page message for a gift the user cannot add again', async () => {
    serve({ ...asAda, 'GET /api/v2/gifts/70': { status: 200, body: received({ can_add_again: false }) } })
    open('/gifts/70/add-again')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})
