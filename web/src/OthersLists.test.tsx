import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Item, List, User } from './api'
import { ada, adaList, heading, kettle, ME, open, page, problem, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

// Mom is the signed-in user. Emma is her child. Sue shares a group with Mom.
const mom: User = {
  ...ada,
  id: '20',
  name: 'Mom',
  last_name: null,
  is_admin: false,
  can_edit_parents: false,
  children: [{ id: '30', name: 'Emma', avatar: null }],
  primary_list: { id: '21', name: 'Mom' },
}
const momList: List = { ...adaList, id: '21', name: 'Mom', recipients: [{ id: '20', name: 'Mom', avatar: null }], item_count: 1 }
const sue: User = {
  ...ada,
  id: '40',
  name: 'Sue',
  last_name: null,
  good_to_know: 'Size **medium**. Allergic to wool.',
  is_admin: false,
  can_edit: false,
  can_make_login_link: false,
  can_manage_sessions: false,
  can_edit_parents: false,
  can_deactivate: false,
  primary_list: { id: '41', name: 'Sue' },
}
const sueList: List = {
  ...adaList,
  id: '41',
  name: 'Sue',
  recipients: [{ id: '40', name: 'Sue', avatar: null }],
  item_count: 2,
  can_edit: false,
}
const scarf: Item = { ...kettle, id: '51', list_id: '41', title: 'A scarf', position: 0, added_by: { id: '40', name: 'Sue', avatar: null }, can_edit: false, can_delete: false }
const book: Item = { ...scarf, id: '52', title: 'A book', position: 1, price_minor: 1999, currency: 'USD' }
const garden: List = {
  ...adaList,
  id: '42',
  name: 'Garden',
  is_primary: false,
  recipients: [
    { id: '40', name: 'Sue', avatar: null },
    { id: '60', name: 'Tom', avatar: null },
  ],
  item_count: 3,
  can_edit: false,
}
const asMom = { [ME]: { status: 200, body: mom } }
const sues = {
  'GET /api/v2/users/40': { status: 200, body: sue },
  'GET /api/v2/lists/41': { status: 200, body: sueList },
  'GET /api/v2/lists/41/items': page([scarf, book]),
  'GET /api/v2/lists?recipient_id=40&is_primary=false': page([garden]),
}

describe("another user's page", () => {
  test("shows the user's good to know, their primary list's items, and their other lists", async () => {
    serve({ ...asMom, ...sues })
    open('/users/40')

    expect(await heading('Sue')).toBeInTheDocument()
    const note = await screen.findByRole('region', { name: 'Good to know' })
    expect(within(note).getByText('medium').tagName).toBe('STRONG')
    const items = within(await screen.findByRole('list', { name: 'Items' })).getAllByRole('listitem')
    expect(items.map((row) => row.textContent)).toEqual(['A scarf', 'A book$19.99'])
    expect(within(items[1]!).getByRole('link')).toHaveAttribute('href', '/items/52')
    const others = within(await screen.findByRole('region', { name: 'Other lists' })).getAllByRole('listitem')
    expect(others.map((row) => row.textContent)).toEqual(['Garden3 items'])
    expect(within(others[0]!).getByRole('link')).toHaveAttribute('href', '/lists/42')
  })

  test('shows no edit controls to a giver, and no For part', async () => {
    serve({ ...asMom, ...sues })
    open('/users/40')

    await screen.findByRole('list', { name: 'Items' })
    expect(screen.queryByRole('link', { name: 'Add' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Change order' })).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'For Sue' })).not.toBeInTheDocument()
  })

  test("shows a parent the edit controls on a child's list, from can_edit", async () => {
    const user = userEvent.setup()
    const emma: User = { ...sue, id: '30', name: 'Emma', good_to_know: null, can_edit: true, primary_list: { id: '31', name: 'Emma' } }
    const emmaList: List = { ...sueList, id: '31', name: 'Emma', recipients: [{ id: '30', name: 'Emma', avatar: null }], can_edit: true }
    const bike: Item = { ...scarf, id: '53', list_id: '31', title: 'A bike', can_edit: true, can_delete: true }
    const bell: Item = { ...bike, id: '54', title: 'A bell', position: 1 }
    serve({
      ...asMom,
      'GET /api/v2/users/30': { status: 200, body: emma },
      'GET /api/v2/lists/31': { status: 200, body: emmaList },
      'GET /api/v2/lists/31/items': page([bike, bell]),
      'GET /api/v2/lists?recipient_id=30&is_primary=false': page([]),
    })
    open('/users/30')

    await screen.findByRole('list', { name: 'Items' })
    expect(screen.getByRole('link', { name: 'Add' })).toHaveAttribute('href', '/lists/31/add')
    expect(screen.queryByRole('region', { name: 'Good to know' })).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Other lists' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Change order' }))
    expect(screen.getByRole('button', { name: 'Move down: A bike' })).toBeInTheDocument()
  })

  test("says so when the user's list is empty", async () => {
    serve({ ...asMom, ...sues, 'GET /api/v2/lists/41/items': page([]) })
    open('/users/40')

    expect(await screen.findByRole('heading', { name: 'This list is empty' })).toBeInTheDocument()
  })

  test('shows none of the primary list to a viewer who gets no primary_list, as the admin does', async () => {
    const hedy: User = { ...sue, id: '70', name: 'Hedy', good_to_know: 'Likes tea', primary_list: undefined }
    const attic: List = { ...garden, id: '43', name: 'Attic', recipients: [{ id: '70', name: 'Hedy', avatar: null }, { id: '40', name: 'Sue', avatar: null }] }
    const fetch = serve({
      [ME]: { status: 200, body: ada },
      'GET /api/v2/users/70': { status: 200, body: hedy },
      'GET /api/v2/lists?recipient_id=70&is_primary=false': page([attic]),
    })
    open('/users/70')

    expect(await heading('Hedy')).toBeInTheDocument()
    expect(await screen.findByRole('region', { name: 'Other lists' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Good to know' })).toHaveTextContent('Likes tea')
    expect(screen.queryByRole('list', { name: 'Items' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'This list is empty' })).not.toBeInTheDocument()
    expect(fetch.mock.calls.some(([input]) => String(input).startsWith('/api/v2/lists/'))).toBe(false)
  })

  test("opens My lists for the user's own page", async () => {
    serve({ ...asMom, 'GET /api/v2/lists?can_edit=true': page([momList]), 'GET /api/v2/lists?recipient_id=30': page([]), 'GET /api/v2/users': page([mom]) })
    open('/users/20')

    expect(await heading('My lists')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/lists')
  })
})

describe('My lists with children', () => {
  const emmaList: List = { ...adaList, id: '31', name: 'Emma', recipients: [{ id: '30', name: 'Emma', avatar: null }], item_count: 2 }
  const cousins: List = { ...adaList, id: '32', name: 'Cousins', is_primary: false, recipients: [{ id: '30', name: 'Emma', avatar: null }], item_count: 0 }
  const house: List = {
    ...adaList,
    id: '33',
    name: 'Our house',
    is_primary: false,
    recipients: [
      { id: '20', name: 'Mom', avatar: null },
      { id: '30', name: 'Emma', avatar: null },
    ],
    item_count: 4,
  }
  const emma: User = { ...sue, id: '30', name: 'Emma', can_edit: true, primary_list: { id: '31', name: 'Emma' } }
  const mine = {
    ...asMom,
    'GET /api/v2/lists?can_edit=true': page([cousins, emmaList, house, momList]),
    'GET /api/v2/lists?recipient_id=30': page([cousins, emmaList, house]),
    'GET /api/v2/users': page([mom, emma]),
    'GET /api/v2/invitations?user_id=20&user_id=30': page([]),
  }

  test("has a part for each child, with the child's primary list first, and a shared list only once", async () => {
    serve(mine)
    open('/lists')

    await heading('My lists')
    const own = await screen.findByRole('list', { name: 'Your lists' })
    expect(within(own).getAllByRole('listitem').map((row) => row.textContent)).toEqual(['Mom1 item', 'Our house4 items'])
    const part = await screen.findByRole('region', { name: 'Emma' })
    const rows = within(part).getAllByRole('listitem')
    expect(rows.map((row) => row.textContent)).toEqual(['Emma2 items', 'CousinsNo items'])
    expect(within(rows[0]!).getByRole('link')).toHaveAttribute('href', '/lists/31')
  })

  test("still shows a deactivated child's part, labelled, from the child's recipient filter", async () => {
    serve({ ...mine, 'GET /api/v2/lists?can_edit=true': page([momList]), 'GET /api/v2/users': page([mom, { ...emma, deactivated_at: '2026-11-09T12:00:00Z' }]) })
    open('/lists')

    await heading('My lists')
    const part = await screen.findByRole('region', { name: /Emma/ })
    expect(within(part).getByRole('heading', { name: 'Emma Deactivated' })).toBeInTheDocument()
    expect(within(part).getAllByRole('listitem')).toHaveLength(2)
  })

  test('has no part for a user with no children', async () => {
    serve({ [ME]: { status: 200, body: { ...mom, children: [] } }, 'GET /api/v2/lists?can_edit=true': page([momList]) })
    open('/lists')

    await screen.findByRole('list', { name: 'Your lists' })
    expect(screen.queryByRole('region', { name: 'Emma' })).not.toBeInTheDocument()
  })

  test("shows the child's part's failure in its place, and keeps the rest", async () => {
    serve({ ...mine, 'GET /api/v2/lists?recipient_id=30': problem(500, 'internal_error') })
    open('/lists')

    expect(await screen.findByRole('list', { name: 'Your lists' })).toBeInTheDocument()
    expect(await screen.findByRole('alert')).toHaveTextContent("Could not load Emma's lists. Reload and try again.")
  })
})

describe('Added by', () => {
  const shown = (item: Item, list: List, seen: User[]) => ({
    ...asMom,
    [`GET /api/v2/items/${item.id}`]: { status: 200, body: item },
    [`GET /api/v2/lists/${item.list_id}`]: { status: 200, body: list },
    'GET /api/v2/users': page(seen),
  })

  test("names the parent who added an item to a child's list, with a link when the user can see them", async () => {
    const emmaList: List = { ...adaList, id: '31', name: 'Emma', recipients: [{ id: '30', name: 'Emma', avatar: null }] }
    const item: Item = { ...kettle, id: '55', list_id: '31', added_by: { id: '20', name: 'Mom', avatar: null } }
    serve(shown(item, emmaList, [mom]))
    open('/items/55')

    const line = await screen.findByText(/Added by/)
    expect(within(line).getByRole('link', { name: 'Mom' })).toHaveAttribute('href', '/users/20')
  })

  test('names who added it with no link when the user cannot see them', async () => {
    const item: Item = { ...scarf, added_by: { id: '99', name: 'Grandma', avatar: null } }
    serve(shown(item, sueList, [mom, sue]))
    open('/items/51')

    expect(await screen.findByText('Added by Grandma')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Grandma' })).not.toBeInTheDocument()
  })

  test('does not say it when the adder is the list\'s only recipient, and shows no edit controls to a giver', async () => {
    serve(shown(scarf, sueList, [mom, sue]))
    open('/items/51')

    await screen.findAllByRole('link', { name: 'Sue' })
    expect(screen.queryByText(/Added by/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()
  })

  test('is not on the rows of a list', async () => {
    serve({ ...asMom, 'GET /api/v2/lists/41': { status: 200, body: sueList }, 'GET /api/v2/lists/41/items': page([{ ...scarf, added_by: { id: '99', name: 'Grandma', avatar: null } }]), 'GET /api/v2/users': page([mom, sue]) })
    open('/lists/41')

    await screen.findByRole('list', { name: 'Items' })
    expect(screen.queryByText(/Added by/)).not.toBeInTheDocument()
  })
})

