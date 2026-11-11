import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Item, SearchResult } from './api'
import { DuplicateHint } from './DuplicateHint'
import { ada, adaList, heading, kettle, ME, open, page, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const onList: Item = {
  ...kettle,
  store_links: [
    {
      id: '31',
      url: 'https://shop.example/kettle',
      store: 'shop.example',
      price_minor: null,
      currency: null,
      captured_at: null,
      image_url: null,
      created_at: '2026-11-08T12:30:00Z',
      can_edit: true,
      can_delete: true,
    },
  ],
}
const grill: Item = { ...kettle, id: '22', title: 'Grill', position: 1 }
const asAda = { [ME]: { status: 200, body: ada }, 'GET /api/v2/groups?includes_me=true': page([]) }
const listRoutes = {
  'GET /api/v2/lists/11': { status: 200, body: adaList },
  'GET /api/v2/lists/11/items': page([onList, grill]),
  'GET /api/v2/users': page([ada]),
}
const calls = (fetch: ReturnType<typeof serve>) => fetch.mock.calls.map(([input, init]) => `${init?.method ?? 'GET'} ${String(input)}`)

describe('the duplicate hint on the add screen', () => {
  test('says one line that the title is already on the list, ignoring case and extra spaces, and opens the item', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, ...listRoutes })
    open('/lists/11/add')
    await heading('Add an item')

    await user.type(screen.getByLabelText('Title'), '  a   KETTLE ')

    const hint = await screen.findByRole('link', { name: 'Already on this list: A kettle' })
    expect(hint).toHaveAttribute('href', '/items/21')

    await user.clear(screen.getByLabelText('Title'))
    await user.type(screen.getByLabelText('Title'), 'Mug')
    await waitFor(() => expect(screen.queryByRole('link', { name: /Already on this list/ })).not.toBeInTheDocument())
  })

  test('says it for a pasted link that is already a store link of an item', async () => {
    const user = userEvent.setup()
    const result: SearchResult = {
      title: 'Copper kettle',
      notes: null,
      notes_cut: false,
      price_minor: null,
      top_price_minor: null,
      currency: null,
      store_links: [{ url: 'https://shop.example/kettle', store: 'shop.example', price_minor: 4999, currency: 'USD', image_url: null }],
      image_urls: [],
      options: [],
      found: true,
    }
    serve({ ...asAda, ...listRoutes, 'POST /api/v2/search': { status: 200, body: { results: [result] } } })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Add an item')

    // The title is new. The address is on the list.
    expect(screen.getByLabelText('Title')).toHaveValue('Copper kettle')
    expect(await screen.findByRole('link', { name: 'Already on this list: A kettle' })).toHaveAttribute('href', '/items/21')
  })

  test('shows nothing for a new title, and never blocks or slows the add', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...asAda, ...listRoutes, 'POST /api/v2/lists/11/items': { status: 201, body: { ...kettle, title: 'A kettle' } } })
    open('/lists/11/add')
    await heading('Add an item')
    await user.type(screen.getByLabelText('Title'), 'a kettle')
    await screen.findByRole('link', { name: /Already on this list/ })

    // The Add button needs no extra tap: one click adds the item, hint or not.
    await user.click(screen.getByRole('button', { name: 'Add' }))

    await waitFor(() => expect(calls(fetch)).toContain('POST /api/v2/lists/11/items'))
  })

  test('adds when the hint could not load', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      ...listRoutes,
      'GET /api/v2/lists/11/items': { status: 500, body: { status: 500, code: 'internal_error' } },
      'POST /api/v2/lists/11/items': { status: 201, body: kettle },
    })
    open('/lists/11/add')
    await heading('Add an item')
    await user.type(screen.getByLabelText('Title'), 'A kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(screen.queryByRole('link', { name: /Already on this list/ })).not.toBeInTheDocument()
    await waitFor(() => expect(calls(fetch)).toContain('POST /api/v2/lists/11/items'))
  })

  test('checks only the list the add goes to', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...asAda, ...listRoutes })
    open('/lists/11/add')
    await heading('Add an item')
    await user.type(screen.getByLabelText('Title'), 'Grill')
    await screen.findByRole('link', { name: 'Already on this list: Grill' })

    expect(calls(fetch).filter((c) => c.includes('/items'))).toEqual(['GET /api/v2/lists/11/items'])
  })
})

describe('DuplicateHint', () => {
  test('loads the items of the list again when the list changes, and checks only that list', async () => {
    const elsewhere: Item = { ...kettle, id: '99', list_id: '12', title: 'Lamp' }
    const fetch = serve({
      'GET /api/v2/lists/11/items': page([onList]),
      'GET /api/v2/lists/12/items': page([elsewhere]),
    })
    const { rerender } = render(<DuplicateHint listId="11" title="Lamp" urls={[]} />)
    await waitFor(() => expect(calls(fetch)).toEqual(['GET /api/v2/lists/11/items']))
    await new Promise((r) => setTimeout(r, 20))
    expect(screen.queryByRole('link')).not.toBeInTheDocument()

    rerender(<DuplicateHint listId="12" title="Lamp" urls={[]} />)

    expect(await screen.findByRole('link', { name: 'Already on this list: Lamp' })).toHaveAttribute('href', '/items/99')
    expect(calls(fetch)).toEqual(['GET /api/v2/lists/11/items', 'GET /api/v2/lists/12/items'])
  })

  test('forgets the old list at once when the list changes', async () => {
    serve({
      'GET /api/v2/lists/11/items': page([onList]),
      'GET /api/v2/lists/12/items': () => ({ status: 200, body: { data: [], next_cursor: null, total_count: 0 } }),
    })
    const { rerender } = render(<DuplicateHint listId="11" title="A kettle" urls={[]} />)
    expect(await screen.findByRole('link', { name: 'Already on this list: A kettle' })).toBeInTheDocument()

    rerender(<DuplicateHint listId="12" title="A kettle" urls={[]} />)

    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })
})
