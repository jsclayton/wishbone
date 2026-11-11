import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Item, List } from './api'
import { ada, adaList, heading, kettle, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const asAda = { [ME]: { status: 200, body: ada } }
const mug: Item = { ...kettle, id: '22', title: 'A mug', position: 1, price_minor: 1250, currency: 'USD', quantity: 3 }
const lamp: Item = { ...kettle, id: '23', title: 'A lamp', position: 2, is_unlimited: true, price_minor: 10000, top_price_minor: 20000, currency: 'USD' }
const attic: List = { ...adaList, id: '31', name: 'Attic', is_primary: false, item_count: 2, can_delete: true }
const home = { 'GET /api/v2/groups?includes_me=true': page([]) }

describe('Home', () => {
  test('has a My lists button, and the Add to my list box', async () => {
    serve({ ...asAda, ...home })
    open('/')

    await heading('Home')
    expect(screen.getByRole('link', { name: 'My lists' })).toHaveAttribute('href', '/lists')
    expect(screen.getByRole('textbox', { name: 'Add to my list' })).toBeInTheDocument()
  })
})

describe('Menu', () => {
  test('has My lists', async () => {
    serve(asAda)
    open('/menu')

    expect(await screen.findByRole('link', { name: 'My lists' })).toHaveAttribute('href', '/lists')
  })
})

describe('the My lists screen', () => {
  test('shows the lists the user can edit, the primary list first, each with its recipients and item count', async () => {
    const fetch = serve({
      ...asAda,
      'GET /api/v2/lists?can_edit=true': page([attic, { ...adaList, item_count: 1 }]),
    })
    open('/lists')

    expect(await heading('My lists')).toBeInTheDocument()
    const rows = await screen.findAllByRole('listitem')
    expect(rows.map((row) => row.textContent)).toEqual(['Ada1 item', 'Attic2 items'])
    expect(within(rows[0]!).getByRole('link')).toHaveAttribute('href', '/lists/11')
    expect(within(rows[1]!).getByRole('link')).toHaveAttribute('href', '/lists/31')
    expect(fetch).toHaveBeenCalledWith('/api/v2/lists?can_edit=true', expect.anything())
  })

  test('follows the cursor to the last page', async () => {
    const fetch = serve({
      ...asAda,
      'GET /api/v2/lists?can_edit=true': page([adaList], 'abc'),
      'GET /api/v2/lists?can_edit=true&cursor=abc': page([attic]),
    })
    open('/lists')

    await heading('My lists')
    expect(await screen.findAllByRole('listitem')).toHaveLength(2)
    expect(fetch).toHaveBeenCalledWith('/api/v2/lists?can_edit=true&cursor=abc', expect.anything())
  })
})

describe('a list', () => {
  const list = (l: List, items: Item[]) => ({
    ...asAda,
    [`GET /api/v2/lists/${l.id}`]: { status: 200, body: l },
    [`GET /api/v2/lists/${l.id}/items`]: page(items),
    'GET /api/v2/users': page([ada]),
  })

  test("shows the name, the recipients, the description as markdown, and the items in the server's order", async () => {
    serve(list({ ...adaList, description: 'We **love** books.', item_count: 3 }, [kettle, mug, lamp]))
    open('/lists/11')

    expect(await heading('Ada')).toBeInTheDocument()
    expect(screen.getByText('love').tagName).toBe('STRONG')
    expect(await screen.findByRole('link', { name: 'Ada' })).toHaveAttribute('href', '/users/7')
    const items = within(await screen.findByRole('list', { name: 'Items' })).getAllByRole('listitem')
    expect(items.map((item) => item.textContent)).toEqual(['A kettle', 'A mugQuantity 3$12.50', 'A lampUnlimited$100-200'])
    expect(within(items[1]!).getByRole('link')).toHaveAttribute('href', '/items/22')
    expect(screen.getByRole('heading', { name: '3 items' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Edit list' })).toHaveAttribute('href', '/lists/11/edit')
    expect(screen.getByRole('link', { name: 'Add' })).toHaveAttribute('href', '/lists/11/add')
  })

  test('says so when it is empty, with no description and no order button', async () => {
    serve(list(adaList, []))
    open('/lists/11')

    expect(await screen.findByRole('heading', { name: 'This list is empty' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Change order' })).not.toBeInTheDocument()
    expect(screen.queryByRole('list', { name: 'Items' })).not.toBeInTheDocument()
  })

  test('shows no edit controls when can_edit is false', async () => {
    serve(list({ ...adaList, can_edit: false }, [kettle, { ...mug, can_edit: false }]))
    open('/lists/11')

    await screen.findByRole('list', { name: 'Items' })
    expect(screen.queryByRole('link', { name: 'Edit list' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Add' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Change order' })).not.toBeInTheDocument()
  })

  test('names a recipient the user cannot see, with no link', async () => {
    serve({ ...list({ ...adaList, recipients: [{ id: '99', name: 'Hedy', avatar: null }] }, []), 'GET /api/v2/users': page([ada]) })
    open('/lists/11')

    await heading('Ada')
    expect(await screen.findByText('Hedy')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Hedy' })).not.toBeInTheDocument()
  })

  test('shows the missing-page message for a list the user cannot see', async () => {
    serve({ ...asAda, 'GET /api/v2/lists/11': problem(404, 'not_found') })
    open('/lists/11')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })

  test('Change order moves an item up or down with its position, and the first and last cannot move off the list', async () => {
    const user = userEvent.setup()
    let items = [kettle, mug, lamp]
    const fetch = serve({
      ...asAda,
      'GET /api/v2/lists/11': { status: 200, body: adaList },
      'GET /api/v2/lists/11/items': () => ({ status: 200, body: { data: items, next_cursor: null, total_count: 3 } }),
      'GET /api/v2/users': page([ada]),
      'PATCH /api/v2/items/22': () => {
        items = [mug, kettle, lamp].map((it, position) => ({ ...it, position }))
        return { status: 200, body: items[0] }
      },
    })
    open('/lists/11')
    await screen.findByRole('list', { name: 'Items' })

    await user.click(screen.getByRole('button', { name: 'Change order' }))
    expect(screen.getByRole('button', { name: 'Move up: A kettle' })).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByRole('button', { name: 'Move down: A lamp' })).toHaveAttribute('aria-disabled', 'true')
    await user.click(screen.getByRole('button', { name: 'Move up: A mug' }))

    await waitFor(() => {
      const rows = within(screen.getByRole('list', { name: 'Items' })).getAllByRole('listitem')
      expect(rows.map((row) => row.querySelector('strong')?.textContent)).toEqual(['A mug', 'A kettle', 'A lamp'])
    })
    expect(sentBody(fetch, 'PATCH /api/v2/items/22')).toEqual({ position: 0 })
    // Done leaves the order mode, and the rows link to their items again.
    await user.click(screen.getByRole('button', { name: 'Done' }))
    expect(screen.getAllByRole('link', { name: /A mug/ })[0]).toHaveAttribute('href', '/items/22')
  })

  test('ignores another tap on Move up or Move down while a move is saving', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...list(adaList, [kettle, mug, lamp]),
      'PATCH /api/v2/items/22': { status: 200, body: { ...mug, position: 0 } },
    })
    const answer = fetch.getMockImplementation()!
    const gate = Promise.withResolvers<void>()
    fetch.mockImplementation(async (input, init) => {
      if (init?.method === 'PATCH') await gate.promise
      return answer(input, init)
    })
    open('/lists/11')
    await screen.findByRole('list', { name: 'Items' })
    await user.click(screen.getByRole('button', { name: 'Change order' }))

    await user.click(screen.getByRole('button', { name: 'Move up: A mug' }))
    await user.click(screen.getByRole('button', { name: 'Move up: A mug' }))
    await user.click(screen.getByRole('button', { name: 'Move down: A kettle' }))
    gate.resolve()

    await waitFor(() => expect(screen.getByRole('button', { name: 'Move down: A kettle' })).toHaveAttribute('aria-disabled', 'false'))
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PATCH')).toHaveLength(1)
  })

  test('Move down sends the next position', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...list(adaList, [kettle, mug]),
      'PATCH /api/v2/items/21': { status: 200, body: { ...kettle, position: 1 } },
    })
    open('/lists/11')
    await screen.findByRole('list', { name: 'Items' })

    await user.click(screen.getByRole('button', { name: 'Change order' }))
    await user.click(screen.getByRole('button', { name: 'Move down: A kettle' }))

    await waitFor(() => expect(sentBody(fetch, 'PATCH /api/v2/items/21')).toEqual({ position: 1 }))
  })
})

describe('the Edit list screen', () => {
  const edit = (l: List = adaList) => ({
    ...asAda,
    'GET /api/v2/lists/11': { status: 200, body: l },
    'GET /api/v2/lists/11/items': page([]),
    'GET /api/v2/users': page([ada]),
  })

  test('starts with the name and the description, saves both, and opens the list', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...edit({ ...adaList, description: 'Old words' }),
      'PATCH /api/v2/lists/11': { status: 200, body: { ...adaList, name: 'Wishes', description: 'New words' } },
    })
    open('/lists/11/edit')
    await heading('Edit list')
    expect(screen.getByLabelText('Name')).toHaveValue('Ada')
    expect(screen.getByLabelText('Description')).toHaveValue('Old words')

    await user.clear(screen.getByLabelText('Name'))
    await user.type(screen.getByLabelText('Name'), 'Wishes')
    await user.clear(screen.getByLabelText('Description'))
    await user.type(screen.getByLabelText('Description'), 'New words')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await heading('Ada')).toBeInTheDocument()
    expect(sentBody(fetch, 'PATCH /api/v2/lists/11')).toEqual({ name: 'Wishes', description: 'New words' })
    expect(window.location.pathname).toBe('/lists/11')
  })

  test('clears the description with null', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...edit({ ...adaList, description: 'Old words' }),
      'PATCH /api/v2/lists/11': { status: 200, body: adaList },
    })
    open('/lists/11/edit')
    await heading('Edit list')

    await user.clear(screen.getByLabelText('Description'))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(sentBody(fetch, 'PATCH /api/v2/lists/11')).toEqual({ name: 'Ada', description: null }))
  })

  test("shows the server's words when it refuses, and keeps what was typed", async () => {
    const user = userEvent.setup()
    serve({ ...edit(), 'PATCH /api/v2/lists/11': problem(422, 'invalid_input', 'A list needs a name.') })
    open('/lists/11/edit')
    await heading('Edit list')

    await user.clear(screen.getByLabelText('Name'))
    await user.type(screen.getByLabelText('Description'), 'Hi')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('A list needs a name.')
    expect(screen.getByLabelText('Description')).toHaveValue('Hi')
  })

  test('is the missing page for a list the user cannot edit', async () => {
    serve(edit({ ...adaList, can_edit: false }))
    open('/lists/11/edit')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('the Add screen of a list', () => {
  const add = {
    ...asAda,
    'GET /api/v2/lists/11': { status: 200, body: adaList },
    'GET /api/v2/lists/11/items': page([kettle]),
    'GET /api/v2/users': page([ada]),
  }

  test('adds an item with a price, a range, notes and a quantity, and opens the list', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...add, 'POST /api/v2/lists/11/items': { status: 201, body: mug } })
    open('/lists/11/add')
    await heading('Add an item')

    await user.type(screen.getByLabelText('Title'), ' A mug ')
    await user.type(screen.getByLabelText('Price'), '12.50')
    await user.type(screen.getByLabelText('Up to (optional)'), '15')
    await user.type(screen.getByLabelText('Notes'), 'Blue')
    await user.clear(screen.getByLabelText('Quantity'))
    await user.type(screen.getByLabelText('Quantity'), '3')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await heading('Ada')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toEqual({
      title: 'A mug',
      notes: 'Blue',
      price_minor: 1250,
      top_price_minor: 1500,
      currency: 'USD',
      quantity: 3,
      is_unlimited: false,
    })
    expect(window.location.pathname).toBe('/lists/11')
  })

  test('adds just a title', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...add, 'POST /api/v2/lists/11/items': { status: 201, body: kettle } })
    open('/lists/11/add')
    await heading('Add an item')

    await user.type(screen.getByLabelText('Title'), 'A kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    await waitFor(() => expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toEqual({ title: 'A kettle', price_minor: null, top_price_minor: null, quantity: 1, is_unlimited: false }))
  })

  test('asks for a title, and for a price it can read, before it sends anything', async () => {
    const user = userEvent.setup()
    const fetch = serve(add)
    open('/lists/11/add')
    await heading('Add an item')

    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter a title.')

    await user.type(screen.getByLabelText('Title'), 'A mug')
    await user.type(screen.getByLabelText('Price'), 'lots')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter the price as a number with up to 2 decimal places, like 12.50.')
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/lists/11/items', expect.objectContaining({ method: 'POST' }))
  })

  test("shows the server's words when it refuses", async () => {
    const user = userEvent.setup()
    serve({ ...add, 'POST /api/v2/lists/11/items': problem(422, 'invalid_input', 'The top price can\'t be below the price.') })
    open('/lists/11/add')
    await heading('Add an item')

    await user.type(screen.getByLabelText('Title'), 'A mug')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await screen.findByRole('alert')).toHaveTextContent("The top price can't be below the price.")
    expect(screen.getByLabelText('Title')).toHaveValue('A mug')
  })

  test('is the missing page for a list the user cannot edit', async () => {
    serve({ ...add, 'GET /api/v2/lists/11': { status: 200, body: { ...adaList, can_edit: false } } })
    open('/lists/11/add')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})
