import { screen, waitFor } from '@testing-library/react'
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
const priced: Item = { ...kettle, price_minor: 4500, top_price_minor: 6000, currency: 'USD', notes: 'Stainless, not plastic.', quantity: 2 }
const wishes: List = { ...adaList, name: 'Wishes' }
const shown = (item: Item = kettle, list: List = wishes) => ({
  ...asAda,
  [`GET /api/v2/items/${item.id}`]: { status: 200, body: item },
  [`GET /api/v2/lists/${item.list_id}`]: { status: 200, body: list },
  'GET /api/v2/users': page([ada]),
})

describe("an item's page", () => {
  test("shows the item, then its list's name, which opens the list, and the recipients", async () => {
    serve(shown(priced))
    open('/items/21')

    expect(await heading('A kettle')).toBeInTheDocument()
    expect(await screen.findByRole('link', { name: 'Wishes' })).toHaveAttribute('href', '/lists/11')
    expect(screen.getByText('$45.00 to $60.00')).toBeInTheDocument()
    expect(screen.getByText('Stainless, not plastic.')).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()
  })

  test('shows the controls from can_edit, and none without it', async () => {
    serve(shown({ ...kettle, can_edit: false, can_delete: false }))
    open('/items/21')

    await heading('A kettle')
    await screen.findByRole('link', { name: 'Wishes' })
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Delete this item' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'One more' })).not.toBeInTheDocument()
  })

  test('says who added it, unless that is the list\'s only recipient or nobody', async () => {
    serve(shown({ ...kettle, added_by: { id: '99', name: 'Mom', avatar: null } }))
    open('/items/21')
    expect(await screen.findByText('Added by Mom')).toBeInTheDocument()
  })

  test('does not say who added it when that is the only recipient', async () => {
    serve(shown(kettle))
    open('/items/21')

    await screen.findByRole('link', { name: 'Wishes' })
    expect(screen.queryByText(/Added by/)).not.toBeInTheDocument()
  })

  test('does not say who added it once that user is deleted', async () => {
    serve(shown({ ...kettle, added_by: null }))
    open('/items/21')

    await screen.findByRole('link', { name: 'Wishes' })
    expect(screen.queryByText(/Added by/)).not.toBeInTheDocument()
  })

  test('shows the missing-page message for an item the user cannot see', async () => {
    serve({ ...asAda, 'GET /api/v2/items/21': problem(404, 'not_found') })
    open('/items/21')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })

  test('How many: One more and One fewer save the quantity, and Unlimited turns it off and on', async () => {
    const user = userEvent.setup()
    let item = priced
    const fetch = serve({
      ...shown(priced),
      'GET /api/v2/items/21': () => ({ status: 200, body: item }),
      'PATCH /api/v2/items/21': (init) => {
        item = { ...item, ...JSON.parse(String(init?.body)) }
        return { status: 200, body: item }
      },
    })
    open('/items/21')
    await screen.findByRole('button', { name: 'One more' })

    await user.click(screen.getByRole('button', { name: 'One more' }))
    await waitFor(() => expect(screen.getByText('3')).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'One fewer' }))
    await waitFor(() => expect(screen.getByText('2')).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: /Unlimited/ }))

    await waitFor(() => expect(screen.getByRole('button', { name: /Unlimited/ })).toHaveAttribute('aria-pressed', 'true'))
    const bodies = fetch.mock.calls.filter(([, init]) => init?.method === 'PATCH').map(([, init]) => JSON.parse(String(init?.body)))
    expect(bodies).toEqual([{ quantity: 3 }, { quantity: 2 }, { is_unlimited: true }])
    expect(screen.getByRole('button', { name: 'One fewer' })).toHaveAttribute('aria-disabled', 'true')
  })

  test('Edit shows the form with the values, saves every field, and shows the item changed', async () => {
    const user = userEvent.setup()
    let item = priced
    const fetch = serve({
      ...shown(priced),
      'GET /api/v2/items/21': () => ({ status: 200, body: item }),
      'PATCH /api/v2/items/21': (init) => {
        item = { ...item, ...JSON.parse(String(init?.body)) }
        return { status: 200, body: item }
      },
    })
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Edit' }))

    expect(screen.getByLabelText('Title')).toHaveValue('A kettle')
    expect(screen.getByLabelText('Price')).toHaveValue('45.00')
    expect(screen.getByLabelText('Up to (optional)')).toHaveValue('60.00')
    expect(screen.getByLabelText('Currency')).toHaveValue('USD')
    expect(screen.getByLabelText('Notes')).toHaveValue('Stainless, not plastic.')
    expect(screen.getByLabelText('Quantity')).toHaveValue(2)

    await user.clear(screen.getByLabelText('Title'))
    await user.type(screen.getByLabelText('Title'), 'A copper kettle')
    await user.clear(screen.getByLabelText('Up to (optional)'))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await heading('A copper kettle')).toBeInTheDocument()
    expect(screen.queryByLabelText('Title')).not.toBeInTheDocument()
    expect(sentBody(fetch, 'PATCH /api/v2/items/21')).toEqual({
      title: 'A copper kettle',
      notes: 'Stainless, not plastic.',
      price_minor: 4500,
      top_price_minor: null,
      currency: 'USD',
      quantity: 2,
      is_unlimited: false,
    })
    expect(screen.getByText('$45.00')).toBeInTheDocument()
  })

  test('clearing the price clears the range and the currency', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...shown(priced), 'PATCH /api/v2/items/21': { status: 200, body: kettle } })
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Edit' }))

    await user.clear(screen.getByLabelText('Price'))
    await user.clear(screen.getByLabelText('Up to (optional)'))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(sentBody(fetch, 'PATCH /api/v2/items/21')).toMatchObject({ price_minor: null, top_price_minor: null, currency: null }),
    )
  })

  test('Cancel leaves the item as it was, and a refusal keeps the form with the server\'s words', async () => {
    const user = userEvent.setup()
    serve({ ...shown(priced), 'PATCH /api/v2/items/21': problem(422, 'invalid_input', 'The top price can\'t be below the price.') })
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Edit' }))

    await user.clear(screen.getByLabelText('Title'))
    await user.type(screen.getByLabelText('Title'), 'Changed')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent("The top price can't be below the price.")
    expect(screen.getByLabelText('Title')).toHaveValue('Changed')

    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getByRole('heading', { level: 1, name: 'A kettle' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Title')).not.toBeInTheDocument()
  })

  test('Delete this item opens the confirmation screen', async () => {
    serve(shown())
    open('/items/21')

    expect(await screen.findByRole('link', { name: 'Delete this item' })).toHaveAttribute('href', '/items/21/delete')
  })
})

describe('the delete confirmation', () => {
  test('says what goes, and the button repeats the action', async () => {
    serve(shown())
    open('/items/21/delete')

    expect(await heading('Delete this item?')).toBeInTheDocument()
    expect(screen.getByText('A kettle comes off the list.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Delete this item' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Keep it' })).toHaveAttribute('href', '/items/21')
  })

  test('deletes the item and opens its list', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...shown(),
      'DELETE /api/v2/items/21': { status: 204 },
      'GET /api/v2/lists/11/items': page([]),
    })
    open('/items/21/delete')
    await user.click(await screen.findByRole('button', { name: 'Delete this item' }))

    expect(await heading('Wishes')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/items/21', expect.objectContaining({ method: 'DELETE' }))
    expect(window.location.pathname).toBe('/lists/11')
  })

  test('stays on the screen when the server fails, so the user can try again', async () => {
    const user = userEvent.setup()
    serve({ ...shown(), 'DELETE /api/v2/items/21': problem(500, 'internal_error') })
    open('/items/21/delete')
    await user.click(await screen.findByRole('button', { name: 'Delete this item' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(window.location.pathname).toBe('/items/21/delete')
  })

  test('is the missing page for an item the user cannot see', async () => {
    serve({ ...asAda, 'GET /api/v2/items/21': problem(404, 'not_found') })
    open('/items/21/delete')
    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})
