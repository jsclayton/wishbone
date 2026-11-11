import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Item, List, User } from './api'
import { ada, adaList, heading, kettle, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

// Mom is the signed-in user, with a child Emma. Her lists: her own, "Our house"
// (Mom and Dad), and "Garden". Emma's: her own and "Cousins". "Trip" has both
// Mom and Emma as recipients, so it shows once, under Mom.
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
const emmaAv = { id: '30', name: 'Emma', avatar: null }
const extra = (id: string, name: string, recipients: List['recipients'], more: Partial<List> = {}): List => ({
  ...adaList,
  id,
  name,
  is_primary: false,
  recipients,
  can_delete: recipients.length === 1,
  ...more,
})
const momList: List = { ...adaList, id: '21', name: 'Mom', recipients: [momAv] }
const emmaList: List = { ...adaList, id: '31', name: 'Emma', recipients: [emmaAv] }
const garden = extra('22', 'Garden', [momAv])
const trip = extra('23', 'Trip', [momAv, emmaAv])
const cousins = extra('32', 'Cousins', [emmaAv])
const everything = [cousins, garden, momList, emmaList, trip]
const asMom = { [ME]: { status: 200, body: mom } }
const emmaUser: User = { ...mom, id: '30', name: 'Emma', children: [], deactivated_at: null } as User

const myLists = {
  ...asMom,
  'GET /api/v2/lists?can_edit=true': page(everything),
  'GET /api/v2/users': page([emmaUser]),
  'GET /api/v2/lists?recipient_id=30': page([cousins, emmaList, trip]),
}

describe('the My lists screen with extra lists', () => {
  test('puts extra lists under the user and under each child, and a list they share once, under the user', async () => {
    serve(myLists)
    open('/lists')

    await heading('My lists')
    const own = await screen.findByRole('list', { name: 'Your lists' })
    expect(within(own).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['MomNo items', 'GardenNo items', 'TripNo items'])
    const emmas = await screen.findByRole('list', { name: "Emma's lists" })
    expect(within(emmas).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['EmmaNo items', 'CousinsNo items'])
  })

  test('ends with a New list button', async () => {
    serve(myLists)
    open('/lists')

    expect(await screen.findByRole('link', { name: 'New list' })).toHaveAttribute('href', '/lists/new')
  })
})

describe('the New list form', () => {
  const created = extra('50', 'Hobbies', [momAv])

  test('asks for a name and a description, saves, and opens the new list', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asMom,
      [ME]: { status: 200, body: { ...mom, children: [] } },
      'POST /api/v2/lists': { status: 201, body: created },
      'GET /api/v2/lists/50': { status: 200, body: created },
      'GET /api/v2/lists/50/items': page([]),
      'GET /api/v2/users': page([]),
    })
    open('/lists/new')

    await heading('New list')
    expect(screen.queryByRole('group', { name: 'Who is it for?' })).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('Name'), '  Hobbies ')
    await user.type(screen.getByLabelText(/Description/), 'Yarn and **paint**')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await heading('Hobbies')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/lists')).toEqual({ name: 'Hobbies', description: 'Yarn and **paint**' })
    expect(window.location.pathname).toBe('/lists/50')
  })

  test('asks who the list is for when the user has children, and sends the child', async () => {
    const user = userEvent.setup()
    const forEmma = extra('51', 'Bike', [emmaAv])
    const fetch = serve({
      ...asMom,
      'GET /api/v2/users': page([emmaUser]),
      'POST /api/v2/lists': { status: 201, body: forEmma },
      'GET /api/v2/lists/51': { status: 200, body: forEmma },
      'GET /api/v2/lists/51/items': page([]),
    })
    open('/lists/new')

    await heading('New list')
    const who = await screen.findByRole('group', { name: 'Who is it for?' })
    expect(within(who).getByRole('radio', { name: 'Me' })).toBeChecked()
    await user.click(within(who).getByRole('radio', { name: 'Emma' }))
    await user.type(screen.getByLabelText('Name'), 'Bike')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await heading('Bike')
    expect(sentBody(fetch, 'POST /api/v2/lists')).toEqual({ name: 'Bike', recipient_id: '30' })
  })

  test('does not offer a deactivated child', async () => {
    serve({
      ...asMom,
      'GET /api/v2/users': page([{ ...emmaUser, deactivated_at: '2026-11-09T12:00:00Z' }]),
    })
    open('/lists/new')

    await heading('New list')
    await waitFor(() => expect(screen.queryByRole('group', { name: 'Who is it for?' })).not.toBeInTheDocument())
  })

  test('needs a name, and shows the words of a refusal', async () => {
    const user = userEvent.setup()
    serve({ ...asMom, [ME]: { status: 200, body: { ...mom, children: [] } }, 'POST /api/v2/lists': problem(422, 'invalid_input', 'The name must be at most 100 characters.') })
    open('/lists/new')

    await heading('New list')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter a name.')

    await user.type(screen.getByLabelText('Name'), 'Too long')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('The name must be at most 100 characters.'))
    expect(window.location.pathname).toBe('/lists/new')
  })
})

describe('Delete list', () => {
  const shown = (l: List) => ({
    ...asMom,
    [`GET /api/v2/lists/${l.id}`]: { status: 200, body: l },
    [`GET /api/v2/lists/${l.id}/items`]: page([]),
    'GET /api/v2/users': page([]),
  })

  test('shows on the list page from can_delete, and opens the confirmation', async () => {
    serve(shown(garden))
    open('/lists/22')

    expect(await screen.findByRole('link', { name: 'Delete list' })).toHaveAttribute('href', '/lists/22/delete')
  })

  test('is not on a list that cannot be deleted', async () => {
    serve(shown({ ...trip, can_delete: false }))
    open('/lists/23')

    await heading('Trip')
    expect(screen.queryByRole('link', { name: 'Delete list' })).not.toBeInTheDocument()
  })

  test('the confirmation says what goes, never mentions gifts, and its button repeats the action', async () => {
    serve(shown(garden))
    open('/lists/22/delete')

    expect(await heading('Delete this list?')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Delete Garden and its items' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Keep it' })).toHaveAttribute('href', '/lists/22')
    expect(document.body.textContent).not.toMatch(/gift|claim/i)
  })

  test('deletes the list and opens My lists', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...shown(garden),
      'DELETE /api/v2/lists/22': { status: 204 },
      'GET /api/v2/lists?can_edit=true': page([momList]),
      'GET /api/v2/users': page([emmaUser]),
      'GET /api/v2/lists?recipient_id=30': page([emmaList]),
    })
    open('/lists/22/delete')
    await user.click(await screen.findByRole('button', { name: 'Delete Garden and its items' }))

    expect(await heading('My lists')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/lists/22', expect.objectContaining({ method: 'DELETE' }))
    expect(window.location.pathname).toBe('/lists')
  })

  test('is the missing page for a list the user cannot delete', async () => {
    serve(shown({ ...garden, can_delete: false }))
    open('/lists/22/delete')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('the List choice on the add screen', () => {
  const seenItems = (id: string, items: Item[]) => ({ [`GET /api/v2/lists/${id}/items`]: page(items) })
  const addTo = (id: string, l: List) => ({ [`GET /api/v2/lists/${id}`]: { status: 200, body: l } })

  test('is left out when the user can edit only one list', async () => {
    serve({ ...asMom, ...addTo('21', momList), ...seenItems('21', []), 'GET /api/v2/lists?can_edit=true': page([momList]) })
    open('/lists/21/add')

    await heading('Add an item')
    expect(await screen.findByRole('link', { name: 'Mom' })).toHaveAttribute('href', '/lists/21')
    expect(screen.queryByRole('combobox', { name: 'List' })).not.toBeInTheDocument()
  })

  test('lists the lists of My lists in order and starts on the list the add button was on', async () => {
    serve({ ...myLists, ...addTo('22', garden), ...seenItems('22', []) })
    open('/lists/22/add')

    const choice = await screen.findByRole('combobox', { name: 'List' })
    expect(choice).toHaveValue('22')
    const groups = within(choice).getAllByRole('group')
    expect(groups.map((g) => g.getAttribute('label'))).toEqual(['Mom', 'Emma'])
    expect(within(groups[0]!).getAllByRole('option').map((o) => o.textContent)).toEqual(['Mom', 'Garden', 'Trip'])
    expect(within(groups[1]!).getAllByRole('option').map((o) => o.textContent)).toEqual(['Emma', 'Cousins'])
  })

  test("checks the chosen list for a duplicate, and adds to it, then opens it", async () => {
    const user = userEvent.setup()
    const emmasBike: Item = { ...kettle, id: '61', list_id: '32', title: 'A bike' }
    const added: Item = { ...kettle, id: '62', list_id: '32', title: 'A bike' }
    const fetch = serve({
      ...myLists,
      ...addTo('21', momList),
      ...seenItems('21', []),
      ...seenItems('32', [emmasBike]),
      'POST /api/v2/lists/32/items': { status: 201, body: added },
      'GET /api/v2/lists/32': { status: 200, body: cousins },
      'GET /api/v2/users': page([emmaUser]),
    })
    open('/lists/21/add')
    await user.type(await screen.findByLabelText('Title'), 'a bike')
    expect(screen.queryByText(/Already on this list/)).not.toBeInTheDocument()

    await user.selectOptions(await screen.findByRole('combobox', { name: 'List' }), '32')

    expect(await screen.findByRole('link', { name: 'Already on this list: A bike' })).toHaveAttribute('href', '/items/61')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await heading('Cousins')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/lists/32/items')).toMatchObject({ title: 'a bike' })
    expect(window.location.pathname).toBe('/lists/32')
  })
})

describe("a user's page when their primary list does not load", () => {
  test('says so, as Other lists does', async () => {
    const sue: User = { ...ada, id: '40', name: 'Sue', last_name: null, is_admin: false, can_edit: false, primary_list: { id: '41', name: 'Sue' } }
    serve({
      ...asMom,
      'GET /api/v2/users/40': { status: 200, body: sue },
      'GET /api/v2/lists/41': problem(500, 'internal_error'),
      'GET /api/v2/lists?recipient_id=40&is_primary=false': page([]),
    })
    open('/users/40')

    await heading('Sue')
    expect(await screen.findByRole('alert')).toHaveTextContent("Could not load Sue's list. Reload and try again.")
  })
})

describe('moving an item', () => {
  const rake: Item = { ...kettle, id: '71', list_id: '22', title: 'A rake' }
  const onItem = (item: Item, list: List, lists: List[] = everything) => ({
    ...asMom,
    [`GET /api/v2/items/${item.id}`]: { status: 200, body: item },
    [`GET /api/v2/lists/${list.id}`]: { status: 200, body: list },
    'GET /api/v2/lists?can_edit=true': page(lists),
    'GET /api/v2/users': page([emmaUser]),
  })

  test("the edit form has a List choice that starts on the item's own list, in the order of My lists", async () => {
    const user = userEvent.setup()
    serve(onItem(rake, garden))
    open('/items/71')
    await user.click(await screen.findByRole('button', { name: 'Edit' }))

    const choice = await screen.findByRole('combobox', { name: 'List' })
    expect(choice).toHaveValue('22')
    const groups = within(choice).getAllByRole('group')
    expect(groups.map((g) => g.getAttribute('label'))).toEqual(['Mom', 'Emma'])
    expect(within(groups[1]!).getAllByRole('option').map((o) => o.textContent)).toEqual(['Emma', 'Cousins'])
  })

  test('has no List choice when the user can edit only one list', async () => {
    const user = userEvent.setup()
    serve(onItem(rake, garden, [garden]))
    open('/items/71')
    await user.click(await screen.findByRole('button', { name: 'Edit' }))

    await screen.findByLabelText('Title')
    expect(screen.queryByRole('combobox', { name: 'List' })).not.toBeInTheDocument()
  })

  test('moves the item with list_id, with no warning, and the item shows its new list', async () => {
    const user = userEvent.setup()
    const moved: Item = { ...rake, list_id: '32' }
    const fetch = serve({
      ...onItem(rake, garden),
      'PATCH /api/v2/items/71': { status: 200, body: moved },
      'GET /api/v2/lists/32': { status: 200, body: cousins },
    })
    open('/items/71')
    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    await user.selectOptions(await screen.findByRole('combobox', { name: 'List' }), '32')
    expect(document.body.textContent).not.toMatch(/gift|claim|warn/i)
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('link', { name: 'Cousins' })).toHaveAttribute('href', '/lists/32')
    expect(sentBody(fetch, 'PATCH /api/v2/items/71')).toMatchObject({ list_id: '32', title: 'A rake' })
    expect(screen.queryByLabelText('Title')).not.toBeInTheDocument()
  })

  test('sends no list_id when the list is not changed', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...onItem(rake, garden), 'PATCH /api/v2/items/71': { status: 200, body: rake } })
    open('/items/71')
    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    await screen.findByRole('combobox', { name: 'List' })
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(screen.queryByLabelText('Title')).not.toBeInTheDocument())
    expect(sentBody(fetch, 'PATCH /api/v2/items/71')).not.toHaveProperty('list_id')
  })

  test("shows the server's words when the user cannot edit the other list, and keeps the form", async () => {
    const user = userEvent.setup()
    serve({
      ...onItem(rake, garden),
      'PATCH /api/v2/items/71': problem(403, 'cannot_edit_target_list', "You can't edit the list to move the item to."),
    })
    open('/items/71')
    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    await user.selectOptions(await screen.findByRole('combobox', { name: 'List' }), '32')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toHaveTextContent("You can't edit the list to move the item to.")
    expect(screen.getByRole('combobox', { name: 'List' })).toHaveValue('32')
  })
})
