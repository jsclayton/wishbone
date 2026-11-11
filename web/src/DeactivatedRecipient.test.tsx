import { screen, within } from '@testing-library/react'
import type { Item, List, Recipient, User } from './api'
import { ada, adaList, heading, kettle, ME, open, page, serve } from './test/fake'

// A deactivated recipient (docs/spec.md, Deactivation and deletion, Screens;
// Lists, Screens). Mom is the signed-in user. Emma is her child, and is
// deactivated. Mom sees Emma and her `deactivated_at`, and none of her `can_*`
// is true, so the page shows no control.

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const paused = '2026-11-09T12:00:00Z'
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
const emma: User = {
  ...ada,
  id: '30',
  name: 'Emma',
  last_name: null,
  is_admin: false,
  deactivated_at: paused,
  can_edit: false,
  can_make_login_link: false,
  can_manage_sessions: false,
  can_edit_parents: false,
  can_deactivate: false,
  can_reactivate: false,
  primary_list: { id: '31', name: 'Emma' },
}
const shared: List = {
  ...adaList,
  id: '33',
  name: 'Shared',
  is_primary: false,
  recipients: [
    { id: '30', name: 'Emma', avatar: null },
    { id: '60', name: 'Zed', avatar: null },
  ],
  item_count: 1,
  can_edit: false,
  can_delete: false,
  can_invite: false,
  can_leave: false,
}
const atlas: Item = { ...kettle, id: '51', list_id: '33', title: 'Atlas', can_edit: false, can_delete: false, can_refresh_prices: false, can_claim: false }
const rows: Recipient[] = [
  { user: { id: '30', name: 'Emma', avatar: null }, created_at: '2026-11-08T12:00:00Z', sees_list_after_leaving: true, can_delete: false },
  { user: { id: '60', name: 'Zed', avatar: null }, created_at: '2026-11-08T12:00:00Z', can_delete: false },
]
const asMom = { [ME]: { status: 200, body: mom } }
const theList = {
  ...asMom,
  'GET /api/v2/lists/33': { status: 200, body: shared },
  'GET /api/v2/lists/33/items': page([atlas]),
  'GET /api/v2/lists/33/recipients': page(rows),
  'GET /api/v2/invitations?list_id=33': page([]),
}

describe("a deactivated recipient on a list's page", () => {
  test('shows Deactivated beside the name for a viewer who gets deactivated_at, and no control', async () => {
    serve({ ...theList, 'GET /api/v2/users': page([mom, emma]) })
    open('/lists/33')

    await heading('Shared')
    const link = await screen.findByRole('link', { name: 'Emma' })
    expect(link).toHaveAttribute('href', '/users/30')
    expect(link.closest('span.flex')).toHaveTextContent('Emma Deactivated')
    expect(screen.getByText('Zed').closest('span.flex')).not.toHaveTextContent('Deactivated')
    await screen.findByRole('list', { name: 'Items' })
    for (const name of ['Edit list', 'Add', 'Invite', 'Leave', 'Make Emma leave', 'Delete list']) {
      expect(screen.queryByRole('link', { name })).not.toBeInTheDocument()
    }
    expect(screen.queryByRole('button', { name: 'Change order' })).not.toBeInTheDocument()
  })

  test("still tells a parent who sees the list through the deactivated child what they see of the claims", async () => {
    serve({ ...theList, 'GET /api/v2/users': page([mom, emma]) })
    open('/lists/33')

    await heading('Shared')
    // The parent's powers stop, so can_edit is false, and the claims still show.
    expect(await screen.findByText("You see what's been claimed here. Fully claimed items don't show.")).toBeInTheDocument()
  })

  test('shows no word about it to a viewer who does not get deactivated_at', async () => {
    serve({ ...asMom, 'GET /api/v2/lists/33': { status: 200, body: shared }, 'GET /api/v2/lists/33/items': page([atlas]), 'GET /api/v2/users': page([mom]) })
    open('/lists/33')

    await heading('Shared')
    await screen.findByRole('list', { name: 'Items' })
    expect(screen.getByText('Emma')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Emma' })).not.toBeInTheDocument()
    expect(screen.queryByText('Deactivated')).not.toBeInTheDocument()
  })

  test('shows Deactivated beside the recipient on an item page too', async () => {
    serve({ ...theList, 'GET /api/v2/items/51': { status: 200, body: atlas }, 'GET /api/v2/users': page([mom, emma]) })
    open('/items/51')

    await heading('Atlas')
    const link = await screen.findByRole('link', { name: 'Emma' })
    expect(link.closest('span.flex')).toHaveTextContent('Emma Deactivated')
    expect(within(screen.getByRole('main')).queryByRole('button', { name: /Remove|Delete|Edit/ })).not.toBeInTheDocument()
  })
})

describe("a deactivated user's page", () => {
  test('shows Deactivated inline after the name in the heading', async () => {
    serve({
      ...asMom,
      'GET /api/v2/users/30': { status: 200, body: emma },
      'GET /api/v2/lists/31': { status: 200, body: { ...adaList, id: '31', name: 'Emma', recipients: [{ id: '30', name: 'Emma', avatar: null }], can_edit: false } },
      'GET /api/v2/lists/31/items': page([]),
      'GET /api/v2/lists?recipient_id=30&is_primary=false': page([]),
    })
    open('/users/30')

    const title = await screen.findByRole('heading', { level: 1, name: 'Emma Deactivated' })
    expect(within(title).getByText('Deactivated')).toBeInTheDocument()
    expect(screen.getByText(/Only the admin can reactivate Emma/)).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'For Emma' })).not.toBeInTheDocument()
  })
})
