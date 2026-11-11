import { screen, within } from '@testing-library/react'
import type { Item, List } from './api'
import { aGift, ada, adaList, heading, kettle, ME, open, page, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

// Ada is a giver on Mom's list (list 12). The claim state is a giver's: the keys
// are absent for a recipient.
const mom = { id: '9', name: 'Mom', avatar: null }
const momsList: List = { ...adaList, id: '12', name: 'Mom', is_primary: true, recipients: [mom], item_count: 3, can_edit: false, can_delete: false }
const asGiver = (item: Partial<Item>): Item => ({ ...kettle, list_id: '12', can_edit: false, can_delete: false, can_claim: true, ...item })
const untouched = asGiver({ id: '21', title: 'A kettle', quantity: 5, still_wanted: 5, my_claimed_quantity: 0 })
const some = asGiver({ id: '22', title: 'A mug', quantity: 5, still_wanted: 3, my_claimed_quantity: 0, position: 1 })
const mine = asGiver({ id: '23', title: 'A rug', quantity: 5, still_wanted: 1, my_claimed_quantity: 2, position: 2 })
const always = asGiver({ id: '24', title: 'Socks', is_unlimited: true, still_wanted: null, my_claimed_quantity: 0, position: 3 })

const listPage = (list: List, items: Item[]) => ({
  [ME]: { status: 200, body: ada },
  [`GET /api/v2/lists/${list.id}`]: { status: 200, body: list },
  [`GET /api/v2/lists/${list.id}/items`]: page(items),
  'GET /api/v2/users': page([ada]),
})

describe("a giver's list page", () => {
  test('shows how many are still wanted, "Always wanted" for an unlimited item, and what the giver claimed', async () => {
    serve(listPage(momsList, [untouched, some, mine, always]))
    open('/lists/12')

    const rows = within(await screen.findByRole('list', { name: 'Items' })).getAllByRole('listitem')
    expect(rows.map((row) => row.textContent)).toEqual([
      'A kettle5 of 5 still wanted',
      'A mug3 of 5 still wanted',
      'A rug1 of 5 still wantedYou claimed 2',
      'SocksAlways wanted',
    ])
  })

  test('a recipient sees neither, and keeps the quantity note', async () => {
    const recipient: Item = { ...kettle, id: '30', title: 'A kettle', quantity: 3, can_edit: true }
    serve(listPage(adaList, [recipient]))
    open('/lists/11')

    const rows = within(await screen.findByRole('list', { name: 'Items' })).getAllByRole('listitem')
    expect(rows.map((row) => row.textContent)).toEqual(['A kettleQuantity 3'])
    expect(screen.queryByText(/still wanted/)).not.toBeInTheDocument()
  })

  const asMomsParent = { ...listPage({ ...momsList, can_edit: true }, [untouched]), [ME]: { status: 200, body: { ...ada, children: [mom] } } }

  test('a parent of a recipient who is not a recipient is told that fully claimed items do not show', async () => {
    serve(asMomsParent)
    open('/lists/12')

    await heading('Mom')
    expect(await screen.findByText("You see what's been claimed here. Fully claimed items don't show.")).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Add' })).toBeInTheDocument()
  })

  test('a parent is told also when they cannot edit, because they still see the claims', async () => {
    serve({ ...asMomsParent, 'GET /api/v2/lists/12': { status: 200, body: { ...momsList, can_edit: false } } })
    open('/lists/12')

    await heading('Mom')
    expect(await screen.findByText("You see what's been claimed here. Fully claimed items don't show.")).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Add' })).not.toBeInTheDocument()
  })

  test('a recipient who edits is not told, and neither is a giver who cannot edit', async () => {
    serve(listPage({ ...momsList, can_edit: false }, [untouched]))
    open('/lists/12')

    await screen.findByRole('list', { name: 'Items' })
    expect(screen.queryByText(/Fully claimed items/)).not.toBeInTheDocument()
  })
})

describe("a giver's item page", () => {
  const shown = (item: Item, gifts = [aGift]) => ({
    [ME]: { status: 200, body: ada },
    [`GET /api/v2/items/${item.id}`]: { status: 200, body: item },
    [`GET /api/v2/lists/${item.list_id}`]: { status: 200, body: momsList },
    [`GET /api/v2/gifts?item_id=${item.id}&giver_id=me&state=open`]: page(gifts),
    'GET /api/v2/users': page([ada]),
  })

  test('shows how many are still wanted, the giver\'s own gifts with I won\'t get this, and I\'ll get this', async () => {
    serve(shown({ ...untouched, still_wanted: 2, my_claimed_quantity: 1 }))
    open('/items/21')

    expect(await heading('A kettle')).toBeInTheDocument()
    expect(await screen.findByText('You claimed 1 for First Advent')).toBeInTheDocument()
    expect(screen.getByText('2 of 5 still wanted')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: "I won't get this: First Advent" })).toHaveAttribute('href', '/gifts/60/unclaim?from=item')
    expect(screen.getByRole('link', { name: "I'll get this" })).toHaveAttribute('href', '/items/21/claim')
  })

  test('words a birthday with the recipient, and a gift whose occasion is hidden with its reveal date', async () => {
    const birthday = { ...aGift, id: '61', occasion: { id: '32', kind: 'birthday' as const, name: 'Birthday' }, occasion_name: 'Birthday' }
    const hidden = { ...aGift, id: '62', occasion: null, occasion_name: null, occasion_date: '2027-03-03', quantity: 2 }
    serve(shown({ ...untouched, my_claimed_quantity: 3 }, [birthday, hidden]))
    open('/items/21')

    expect(await screen.findByText("You claimed 1 for Mom's birthday")).toBeInTheDocument()
    expect(screen.getByText('You claimed 2, hidden until Mar 4')).toBeInTheDocument()
  })

  test('names a birthday by its user when the list has several recipients', async () => {
    const dad = { id: '10', name: 'Dad', avatar: null }
    const birthday = {
      ...aGift,
      occasion: { id: '33', kind: 'birthday' as const, name: 'Birthday' },
      occasion_name: 'Birthday',
      recipients: [mom, dad],
    }
    serve({
      ...shown({ ...untouched, my_claimed_quantity: 1 }, [birthday]),
      'GET /api/v2/occasions?list_id=12': page([
        { id: '33', kind: 'birthday', name: 'Birthday', rule: { kind: 'annual', month: 1, day: 1, year: null, weekday: null, n: null }, user: dad, is_everyone: null, next_date: '2027-01-01', can_edit: false, can_delete: false, can_attach_group: false },
      ]),
    })
    open('/items/21')

    expect(await screen.findByText("You claimed 1 for Dad's birthday")).toBeInTheDocument()
  })

  test('offers no I\'ll get this when can_claim is false, and says "Always wanted" for an unlimited item', async () => {
    serve(shown({ ...always, can_claim: false }, []))
    open('/items/24')

    expect(await heading('Socks')).toBeInTheDocument()
    expect(await screen.findByText('Always wanted')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: "I'll get this" })).not.toBeInTheDocument()
  })

  test('a recipient sees no claim state, asks for no gifts, and has no claim controls', async () => {
    const recipient: Item = { ...kettle, id: '21', list_id: '11', can_claim: false }
    const fetch = serve({
      [ME]: { status: 200, body: ada },
      'GET /api/v2/items/21': { status: 200, body: recipient },
      'GET /api/v2/lists/11': { status: 200, body: adaList },
      'GET /api/v2/users': page([ada]),
    })
    open('/items/21')

    await heading('A kettle')
    await screen.findAllByRole('link', { name: 'Ada' })
    expect(screen.queryByText(/still wanted|You claimed|Always wanted/)).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: "I'll get this" })).not.toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith(expect.stringContaining('/api/v2/gifts'), expect.anything())
  })
})
