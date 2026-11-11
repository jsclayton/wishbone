import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Invitation, List, Recipient, User } from './api'
import { ada, adaList, heading, ME, open, page, problem, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

// Mom is the signed-in user, with a child Emma. "Our house" has Mom and Dad.
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
const momAv = { id: '20', name: 'Mom', avatar: null }
const dadAv = { id: '25', name: 'Dad', avatar: null }
const emmaAv = { id: '30', name: 'Emma', avatar: null }
const grandpaAv = { id: '26', name: 'Grandpa', avatar: null }
const extra = (id: string, name: string, recipients: List['recipients'], more: Partial<List> = {}): List => ({
  ...adaList,
  id,
  name,
  is_primary: false,
  recipients,
  ...more,
})
const house = extra('22', 'Our house', [momAv, dadAv], { can_invite: true, can_leave: true })
const emmaUser: User = { ...mom, id: '30', name: 'Emma', children: [], deactivated_at: null } as User
const invitation = (id: string, listId: string, listName: string, user: Invitation['user'], by: Invitation['invited_by'], more: Partial<Invitation> = {}): Invitation => ({
  id,
  list: { id: listId, name: listName },
  user,
  invited_by: by,
  created_at: '2026-11-09T12:00:00Z',
  can_accept: true,
  can_delete: true,
  ...more,
})
const asMom = { [ME]: { status: 200, body: mom } }
const mine = 'GET /api/v2/invitations?user_id=20&user_id=30'
// Home asks for one invitation only, and counts them by the collection's total.
const mineCount = 'GET /api/v2/invitations?user_id=20&user_id=30&limit=1'
const total = (n: number, first: Invitation[]) => ({ status: 200, body: { data: first, next_cursor: n > first.length ? 'more' : null, total_count: n } })

describe('the invitation count on Home', () => {
  const toMom = invitation('1', '22', 'Our house', { id: '20', name: 'Mom', avatar: null }, dadAv)

  test('shows how many invitations the user and their children have, from the total and not the first page', async () => {
    serve({ ...asMom, [mineCount]: total(120, [toMom]), 'GET /api/v2/groups': page([]), 'GET /api/v2/users': page([]) })
    open('/')

    expect(await screen.findByRole('link', { name: 'My lists · 120 invitations' })).toHaveAttribute('href', '/lists')
  })

  test('says "1 invitation" for one, and just "My lists" for none', async () => {
    serve({ ...asMom, [mineCount]: total(1, [toMom]), 'GET /api/v2/groups': page([]), 'GET /api/v2/users': page([]) })
    open('/')
    expect(await screen.findByRole('link', { name: 'My lists · 1 invitation' })).toBeInTheDocument()
  })

  test('is plain My lists with no invitations', async () => {
    serve({ ...asMom, [mineCount]: total(0, []), 'GET /api/v2/groups': page([]), 'GET /api/v2/users': page([]) })
    open('/')

    await heading('Home')
    await waitFor(() => expect(screen.getByRole('link', { name: 'My lists' })).toBeInTheDocument())
  })
})

describe('invitations on My lists', () => {
  const toMom = invitation('1', '22', 'Our house', { id: '20', name: 'Mom', avatar: null }, dadAv)
  const toEmma = invitation('2', '32', 'Cousins', emmaAv, momAv)
  const myLists = {
    ...asMom,
    'GET /api/v2/lists?can_edit=true': page([house]),
    'GET /api/v2/users': page([emmaUser]),
    'GET /api/v2/lists?recipient_id=30': page([]),
    'GET /api/v2/lists/22': { status: 200, body: house },
    'GET /api/v2/lists/32': problem(404, 'not_found'),
  }

  test('shows each invitation for the user and for their child, above the lists', async () => {
    serve({ ...myLists, [mine]: page([toMom, toEmma]) })
    open('/lists')

    await heading('My lists')
    const list = await screen.findByRole('list', { name: 'Invitations' })
    const rows = within(list).getAllByRole('listitem')
    expect(rows.map((r) => r.textContent)).toEqual(['Dad invited you to Our houseAcceptDecline', 'Mom invited Emma to CousinsAcceptDecline'])
    // The text opens the list's page only when the user can see the list.
    expect(await within(rows[0]!).findByRole('link', { name: /Dad invited you to Our house/ })).toHaveAttribute('href', '/lists/22')
    expect(within(rows[1]!).queryByRole('link')).not.toBeInTheDocument()
    expect(list.compareDocumentPosition(screen.getByRole('list', { name: 'Your lists' })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  test('shows no invitations part when none are pending', async () => {
    serve({ ...myLists, [mine]: page([]) })
    open('/lists')

    await heading('My lists')
    await screen.findByRole('list', { name: 'Your lists' })
    expect(screen.queryByRole('list', { name: 'Invitations' })).not.toBeInTheDocument()
  })

  test('Accept needs no confirmation, accepts, and the list shows in the lists', async () => {
    const user = userEvent.setup()
    let accepted = false
    const fetch = serve({
      ...myLists,
      [mine]: () => page(accepted ? [toEmma] : [toMom, toEmma]),
      'GET /api/v2/lists?can_edit=true': () => page(accepted ? [house, extra('40', 'New one', [momAv])] : [house]),
      'POST /api/v2/invitations/1/accept': () => {
        accepted = true
        return { status: 200, body: house }
      },
    })
    open('/lists')
    const row = (await screen.findByRole('link', { name: /Dad invited you/ })).closest('li')!
    await user.click(within(row).getByRole('button', { name: 'Accept' }))

    await waitFor(() => expect(screen.queryByText(/Dad invited you/)).not.toBeInTheDocument())
    expect(fetch).toHaveBeenCalledWith('/api/v2/invitations/1/accept', expect.objectContaining({ method: 'POST' }))
    expect(await screen.findByText('New one')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/lists')
  })

  test('Decline needs no confirmation and removes the invitation', async () => {
    const user = userEvent.setup()
    let declined = false
    const fetch = serve({
      ...myLists,
      [mine]: () => page(declined ? [toMom] : [toMom, toEmma]),
      'DELETE /api/v2/invitations/2': () => {
        declined = true
        return { status: 204 }
      },
    })
    open('/lists')
    const row = (await screen.findByText(/Mom invited Emma/)).closest('li')!
    await user.click(within(row).getByRole('button', { name: 'Decline' }))

    await waitFor(() => expect(screen.queryByText(/Mom invited Emma/)).not.toBeInTheDocument())
    expect(fetch).toHaveBeenCalledWith('/api/v2/invitations/2', expect.objectContaining({ method: 'DELETE' }))
  })

  test("shows the words of a refusal and keeps the invitation", async () => {
    const user = userEvent.setup()
    serve({ ...myLists, [mine]: page([toMom]), 'POST /api/v2/invitations/1/accept': problem(403, 'user_deactivated', 'This user is deactivated.') })
    open('/lists')
    await user.click(await screen.findByRole('button', { name: 'Accept' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('This user is deactivated.')
    expect(screen.getByText(/Dad invited you/)).toBeInTheDocument()
  })

  test('shows the words of a refused Decline and keeps the invitation', async () => {
    const user = userEvent.setup()
    serve({ ...myLists, [mine]: page([toMom]), 'DELETE /api/v2/invitations/1': problem(403, 'user_deactivated', 'This user is deactivated.') })
    open('/lists')
    await user.click(await screen.findByRole('button', { name: 'Decline' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('This user is deactivated.')
    expect(screen.getByText(/Dad invited you/)).toBeInTheDocument()
  })

  test('shows Accept and Decline from can_accept and can_delete, so a deactivated child has neither', async () => {
    serve({ ...myLists, [mine]: page([{ ...toEmma, can_accept: false, can_delete: false }]) })
    open('/lists')

    const list = await screen.findByRole('list', { name: 'Invitations' })
    expect(within(list).getByText('Mom invited Emma to Cousins')).toBeInTheDocument()
    expect(within(list).queryByRole('button')).not.toBeInTheDocument()
  })

  test("says who invited when the inviter is gone", async () => {
    serve({ ...myLists, [mine]: page([{ ...toMom, invited_by: null }]) })
    open('/lists')

    expect(await screen.findByRole('link', { name: 'You were invited to Our house' })).toBeInTheDocument()
  })
})

describe("a list's page and its invitations", () => {
  const onList = (l: List, invitations: Invitation[], more: Record<string, unknown> = {}) => ({
    ...asMom,
    [`GET /api/v2/lists/${l.id}`]: { status: 200, body: l },
    [`GET /api/v2/lists/${l.id}/items`]: page([]),
    [`GET /api/v2/invitations?list_id=${l.id}`]: page(invitations),
    'GET /api/v2/users': page([emmaUser]),
    ...more,
  })
  const toGrandpa = invitation('3', '22', 'Our house', grandpaAv, momAv)

  test("shows the user's own pending invitation first, with Accept and Decline", async () => {
    const user = userEvent.setup()
    const toMom = invitation('1', '32', 'Cousins', { id: '20', name: 'Mom', avatar: null }, dadAv)
    const cousins = extra('32', 'Cousins', [dadAv], { can_edit: false })
    let accepted = false
    const nowMine = { ...cousins, recipients: [dadAv, momAv], can_edit: true }
    const fetch = serve(
      onList(cousins, [], {
        'GET /api/v2/lists/32': () => ({ status: 200, body: accepted ? nowMine : cousins }),
        'GET /api/v2/invitations?list_id=32': () => page(accepted ? [] : [toMom]),
        'POST /api/v2/invitations/1/accept': () => {
          accepted = true
          return { status: 200, body: nowMine }
        },
      }),
    )
    open('/lists/32')

    expect(await screen.findByText('Dad invited you to this list')).toBeInTheDocument()
    // The invitation is the first thing under the heading.
    const note = screen.getByText('Dad invited you to this list')
    expect(note.compareDocumentPosition(screen.getByText('Dad', { selector: 'span' })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    await user.click(screen.getByRole('button', { name: 'Accept' }))

    await waitFor(() => expect(screen.queryByText('Dad invited you to this list')).not.toBeInTheDocument())
    expect(fetch).toHaveBeenCalledWith('/api/v2/invitations/1/accept', expect.objectContaining({ method: 'POST' }))
    expect(await screen.findByRole('link', { name: 'Add' })).toBeInTheDocument()
  })

  test("shows a child's invitation by the child's name, and Decline", async () => {
    const user = userEvent.setup()
    const cousins = extra('32', 'Cousins', [dadAv], { can_edit: false })
    let declined = false
    const fetch = serve(
      onList(cousins, [], {
        'GET /api/v2/invitations?list_id=32': () => page(declined ? [] : [invitation('2', '32', 'Cousins', emmaAv, dadAv)]),
        'DELETE /api/v2/invitations/2': () => {
          declined = true
          return { status: 204 }
        },
      }),
    )
    open('/lists/32')

    expect(await screen.findByText('Dad invited Emma to this list')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Decline' }))

    await waitFor(() => expect(fetch).toHaveBeenCalledWith('/api/v2/invitations/2', expect.objectContaining({ method: 'DELETE' })))
    await waitFor(() => expect(screen.queryByText('Dad invited Emma to this list')).not.toBeInTheDocument())
  })

  test('shows a recipient the other pending invitations with Withdraw, and Invite', async () => {
    const user = userEvent.setup()
    let withdrawn = false
    const fetch = serve(
      onList(house, [], {
        'GET /api/v2/invitations?list_id=22': () => page(withdrawn ? [] : [toGrandpa]),
        'DELETE /api/v2/invitations/3': () => {
          withdrawn = true
          return { status: 204 }
        },
      }),
    )
    open('/lists/22')

    const others = await screen.findByRole('list', { name: 'Invitations' })
    expect(within(others).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['Grandpa · invited by MomWithdraw'])
    expect(screen.getByRole('link', { name: 'Invite' })).toHaveAttribute('href', '/lists/22/invite')
    await user.click(within(others).getByRole('button', { name: "Withdraw Grandpa's invitation" }))

    await waitFor(() => expect(screen.queryByRole('list', { name: 'Invitations' })).not.toBeInTheDocument())
    expect(fetch).toHaveBeenCalledWith('/api/v2/invitations/3', expect.objectContaining({ method: 'DELETE' }))
  })

  test('shows no Invite where can_invite is false, and no Withdraw where can_delete is false', async () => {
    serve(onList({ ...house, can_invite: false }, [{ ...toGrandpa, can_delete: false }]))
    open('/lists/22')

    await heading('Our house')
    expect(await screen.findByText(/Grandpa/)).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Invite' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Withdraw/ })).not.toBeInTheDocument()
  })

  test('does not show the user\'s own invitation twice', async () => {
    const toMom = invitation('1', '22', 'Our house', { id: '20', name: 'Mom', avatar: null }, dadAv)
    serve(onList({ ...house, can_invite: true }, [toMom, toGrandpa]))
    open('/lists/22')

    await heading('Our house')
    expect(await screen.findByText('Dad invited you to this list')).toBeInTheDocument()
    const others = screen.getByRole('list', { name: 'Invitations' })
    expect(within(others).getAllByRole('listitem')).toHaveLength(1)
  })
})

describe('the Invite screen', () => {
  const sue = { ...emmaUser, id: '40', name: 'Sue' }
  const grace = { ...emmaUser, id: '41', name: 'Grace' }
  const base = {
    ...asMom,
    'GET /api/v2/lists/22': { status: 200, body: house },
    'GET /api/v2/users?invitable_to=22': page([grace, sue]),
    'GET /api/v2/lists/22/items': page([]),
    'GET /api/v2/invitations?list_id=22': page([]),
    'GET /api/v2/users': page([]),
  }

  test('lists the users that can be invited, by name', async () => {
    serve(base)
    open('/lists/22/invite')

    await heading('Invite someone to Our house')
    const buttons = await screen.findAllByRole('button')
    expect(buttons.map((b) => b.getAttribute('aria-label'))).toEqual(['Invite Grace', 'Invite Sue'])
  })

  test('a tap invites with no confirmation and goes back to the list', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...base,
      'POST /api/v2/lists/22/invitations': { status: 201, body: invitation('9', '22', 'Our house', sue, momAv) },
    })
    open('/lists/22/invite')
    await user.click(await screen.findByRole('button', { name: 'Invite Sue' }))

    expect(await heading('Our house')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/lists/22')
    expect(JSON.parse(String(fetch.mock.calls.find(([, init]) => init?.method === 'POST')![1]?.body))).toEqual({ user_id: '40' })
  })

  test('shows the words of a refusal and keeps the list', async () => {
    const user = userEvent.setup()
    serve({ ...base, 'POST /api/v2/lists/22/invitations': problem(409, 'already_invited', 'That user already has an invitation to this list.') })
    open('/lists/22/invite')
    await user.click(await screen.findByRole('button', { name: 'Invite Sue' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('That user already has an invitation to this list.')
    expect(screen.getByRole('button', { name: 'Invite Grace' })).toBeInTheDocument()
  })

  test('says so when nobody can be invited', async () => {
    serve({ ...base, 'GET /api/v2/users?invitable_to=22': page([]) })
    open('/lists/22/invite')

    expect(await screen.findByText('There is nobody you can invite to this list.')).toBeInTheDocument()
  })

  test('is the missing page for a list the user cannot invite to', async () => {
    serve({ ...base, 'GET /api/v2/lists/22': { status: 200, body: { ...house, can_invite: false } } })
    open('/lists/22/invite')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('leaving a list', () => {
  const rows = (...rs: Recipient[]) => ({ 'GET /api/v2/lists/22/recipients': page(rs) })
  const momRow = (more: Partial<Recipient> = {}): Recipient => ({ user: momAv, created_at: '2026-11-09T12:00:00Z', sees_list_after_leaving: false, can_delete: true, ...more })
  const emmaRow = (more: Partial<Recipient> = {}): Recipient => ({ user: emmaAv, created_at: '2026-11-09T12:00:00Z', sees_list_after_leaving: false, can_delete: true, ...more })
  const dadRow: Recipient = { user: dadAv, created_at: '2026-11-09T12:00:00Z', can_delete: false }
  const onList = (l: List, more: Record<string, unknown> = {}) => ({
    ...asMom,
    [`GET /api/v2/lists/${l.id}`]: { status: 200, body: l },
    [`GET /api/v2/lists/${l.id}/items`]: page([]),
    [`GET /api/v2/invitations?list_id=${l.id}`]: page([]),
    'GET /api/v2/users': page([emmaUser]),
    ...more,
  })

  test('shows Leave from can_leave, and opens the confirmation', async () => {
    serve(onList(house))
    open('/lists/22')

    expect(await screen.findByRole('link', { name: 'Leave' })).toHaveAttribute('href', '/lists/22/recipients/20/leave')
  })

  test('shows no Leave where can_leave is false', async () => {
    serve(onList({ ...house, can_leave: false }))
    open('/lists/22')

    await heading('Our house')
    await screen.findByRole('link', { name: 'Add' })
    expect(screen.queryByRole('link', { name: 'Leave' })).not.toBeInTheDocument()
  })

  test("shows Make Emma leave for each child whose row can be deleted", async () => {
    const trip = extra('23', 'Trip', [momAv, emmaAv], { can_leave: true })
    serve(onList(trip, { 'GET /api/v2/lists/23/recipients': page([momRow(), emmaRow(), dadRow]) }))
    open('/lists/23')

    expect(await screen.findByRole('link', { name: 'Make Emma leave' })).toHaveAttribute('href', '/lists/23/recipients/30/leave')
    expect(screen.getByRole('link', { name: 'Leave' })).toBeInTheDocument()
  })

  test("leaves out Make Emma leave when her row can't be deleted", async () => {
    const trip = extra('23', 'Trip', [momAv, emmaAv], { can_leave: true })
    serve(onList(trip, { 'GET /api/v2/lists/23/recipients': page([momRow(), emmaRow({ can_delete: false })]) }))
    open('/lists/23')

    await screen.findByRole('link', { name: 'Leave' })
    await waitFor(() => expect(screen.queryByRole('link', { name: /Make Emma leave/ })).not.toBeInTheDocument())
  })

  test('the confirmation button repeats the action and a leave opens My lists', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...onList(house),
      ...rows(momRow(), dadRow),
      'DELETE /api/v2/lists/22/recipients/20': { status: 204 },
      'GET /api/v2/lists?can_edit=true': page([]),
      'GET /api/v2/lists?recipient_id=30': page([]),
      'GET /api/v2/invitations?user_id=20&user_id=30': page([]),
    })
    open('/lists/22/recipients/20/leave')

    expect(await heading('Leave this list?')).toBeInTheDocument()
    expect(screen.queryByText(/You'll see what's been claimed/)).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Stay on this list' })).toHaveAttribute('href', '/lists/22')
    await user.click(screen.getByRole('button', { name: 'Leave Our house' }))

    expect(await heading('My lists')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/lists/22/recipients/20', expect.objectContaining({ method: 'DELETE' }))
    expect(window.location.pathname).toBe('/lists')
  })

  test('warns a user who would still see the list', async () => {
    serve({ ...onList(house), ...rows(momRow({ sees_list_after_leaving: true }), dadRow) })
    open('/lists/22/recipients/20/leave')

    await heading('Leave this list?')
    expect(screen.getByText("You'll see what's been claimed on this list, including gifts for you.")).toBeInTheDocument()
  })

  test("makes a child leave, warns the parent from the child's row, and opens My lists", async () => {
    const user = userEvent.setup()
    const trip = extra('23', 'Trip', [momAv, emmaAv], { can_leave: true })
    const fetch = serve({
      ...onList(trip),
      'GET /api/v2/lists/23/recipients': page([momRow(), emmaRow({ sees_list_after_leaving: true })]),
      'DELETE /api/v2/lists/23/recipients/30': { status: 204 },
      'GET /api/v2/lists?can_edit=true': page([]),
      'GET /api/v2/lists?recipient_id=30': page([]),
      'GET /api/v2/invitations?user_id=20&user_id=30': page([]),
    })
    open('/lists/23/recipients/30/leave')

    expect(await heading('Make Emma leave this list?')).toBeInTheDocument()
    expect(screen.getByText("Emma will see what's been claimed on this list, including gifts for Emma.")).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Make Emma leave Trip' }))

    expect(await heading('My lists')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/lists/23/recipients/30', expect.objectContaining({ method: 'DELETE' }))
  })

  test('is the missing page for a recipient the user cannot make leave', async () => {
    serve({ ...onList(house), ...rows(momRow(), dadRow) })
    open('/lists/22/recipients/25/leave')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })

  test('shows a failed leave and stays', async () => {
    const user = userEvent.setup()
    serve({ ...onList(house), ...rows(momRow(), dadRow), 'DELETE /api/v2/lists/22/recipients/20': problem(500, 'internal_error') })
    open('/lists/22/recipients/20/leave')
    await user.click(await screen.findByRole('button', { name: 'Leave Our house' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(window.location.pathname).toBe('/lists/22/recipients/20/leave')
  })
})
