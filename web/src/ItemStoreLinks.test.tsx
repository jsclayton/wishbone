import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ImageSummary, Item, StoreLink } from './api'
import { ada, adaList, heading, kettle, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const link = (id: string, store: string, over: Partial<StoreLink> = {}): StoreLink => ({
  id,
  url: `https://${store}/kettle`,
  store,
  price_minor: null,
  currency: null,
  captured_at: null,
  image_url: null,
  created_at: '2026-11-08T12:30:00Z',
  can_edit: true,
  can_delete: true,
  ...over,
})
const image = (id: string): ImageSummary => ({ id, url: `/api/v2/images/${id}`, width: 40, height: 30 })

const shop = link('31', 'shop.example', { price_minor: 4999, currency: 'USD', captured_at: '2026-11-08T12:00:00Z' })
const other = link('32', 'other.example')
const withLinks = (...links: StoreLink[]): Item => ({ ...kettle, store_links: links, can_refresh_prices: links.length > 0 })

const asAda = { [ME]: { status: 200, body: ada } }
/** The item's routes, with the item held in `state` so a write can change it. */
function itemServer(state: { item: Item }, more: Parameters<typeof serve>[0] = {}) {
  return serve({
    ...asAda,
    'GET /api/v2/items/21': () => ({ status: 200, body: state.item }),
    'GET /api/v2/lists/11': { status: 200, body: adaList },
    'GET /api/v2/users': page([ada]),
    ...more,
  })
}

describe("an item's images, notes and store links", () => {
  test('shows every image with the primary image first, and the notes', async () => {
    const item: Item = { ...kettle, notes: 'Stainless.', images: [image('1'), image('2'), image('3')], primary_image: image('2') }
    itemServer({ item })
    open('/items/21')

    const pictures = await screen.findByRole('region', { name: 'Images' })
    const imgs = within(pictures).getAllByRole('img')
    expect(imgs.map((i) => i.getAttribute('src'))).toEqual(['/api/v2/images/2', '/api/v2/images/1', '/api/v2/images/3'])
    expect(imgs[0]).toHaveAccessibleName('A kettle: 1 of 3, primary')
    expect(screen.getByText('Stainless.')).toBeInTheDocument()
  })

  test('shows no images section for an item without images, to a reader', async () => {
    itemServer({ item: { ...kettle, can_edit: false, can_delete: false } })
    open('/items/21')

    await heading('A kettle')
    expect(screen.queryByRole('region', { name: 'Images' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /image/i })).not.toBeInTheDocument()
  })

  test('lists the store links with their price and date, each opening its page in a new tab', async () => {
    itemServer({ item: withLinks(shop, other) })
    open('/items/21')

    const where = await screen.findByRole('region', { name: 'Where to buy' })
    const first = within(where).getByRole('link', { name: 'shop.example' })
    expect(first).toHaveAttribute('href', 'https://shop.example/kettle')
    expect(first).toHaveAttribute('target', '_blank')
    expect(first).toHaveAttribute('rel', expect.stringContaining('noreferrer'))
    expect(within(where).getByText('$49.99')).toBeInTheDocument()
    expect(within(where).getByText('Nov 8')).toBeInTheDocument()
    expect(within(where).getByRole('link', { name: 'other.example' })).toBeInTheDocument()
  })

  test('shows a reader the store links and no way to change them', async () => {
    itemServer({ item: { ...withLinks(shop), can_edit: false, can_delete: false, can_refresh_prices: false, store_links: [{ ...shop, can_edit: false, can_delete: false }] } })
    open('/items/21')

    const where = await screen.findByRole('region', { name: 'Where to buy' })
    expect(within(where).getByRole('link', { name: 'shop.example' })).toBeInTheDocument()
    expect(within(where).queryByRole('button')).not.toBeInTheDocument()
  })

  test('shows no Where to buy section to a reader when there are no store links', async () => {
    itemServer({ item: { ...kettle, can_edit: false, can_delete: false } })
    open('/items/21')

    await heading('A kettle')
    expect(screen.queryByRole('region', { name: 'Where to buy' })).not.toBeInTheDocument()
  })
})

describe('adding a store link', () => {
  test('posts the link, shows the new store link with its price, and closes the form', async () => {
    const user = userEvent.setup()
    const state = { item: kettle }
    const fetch = itemServer(state, {
      'POST /api/v2/items/21/store-links': () => {
        state.item = withLinks(shop)
        return { status: 201, body: shop }
      },
    })
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Add link' }))

    await user.type(screen.getByLabelText('Link'), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Save link' }))

    expect(await screen.findByRole('link', { name: 'shop.example' })).toBeInTheDocument()
    expect(screen.getByText('$49.99')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/items/21/store-links')).toEqual({ url: 'https://shop.example/kettle' })
    expect(screen.queryByLabelText('Link')).not.toBeInTheDocument()
  })

  test('says why a link from a blocked store was not added, and keeps the form', async () => {
    const user = userEvent.setup()
    itemServer(
      { item: kettle },
      { 'POST /api/v2/items/21/store-links': problem(422, 'store_blocked', "Links from temu.com can't be added. Ask the admin if you think this is a mistake.") },
    )
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Add link' }))
    await user.type(screen.getByLabelText('Link'), 'https://temu.com/p')
    await user.click(screen.getByRole('button', { name: 'Save link' }))

    expect(await screen.findByRole('alert')).toHaveTextContent("Links from temu.com can't be added")
    expect(screen.getByLabelText('Link')).toHaveValue('https://temu.com/p')
  })

  test('says so when the hourly limit stopped the read', async () => {
    const user = userEvent.setup()
    itemServer({ item: kettle }, { 'POST /api/v2/items/21/store-links': problem(429, 'rate_limited', 'You have looked up a lot of links and photos. Try again later.') })
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Add link' }))
    await user.type(screen.getByLabelText('Link'), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Save link' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Try again later')
  })

  test('Cancel closes the form without adding', async () => {
    const user = userEvent.setup()
    const fetch = itemServer({ item: kettle })
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Add link' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(screen.queryByLabelText('Link')).not.toBeInTheDocument()
    expect(fetch.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false)
  })

  test('turns Add link off at ten store links', async () => {
    const ten = Array.from({ length: 10 }, (_, i) => link(String(40 + i), `s${i}.example`))
    itemServer({ item: withLinks(...ten) })
    open('/items/21')

    expect(await screen.findByRole('button', { name: 'Add link' })).toBeDisabled()
  })
})

describe('changing a store link', () => {
  test("sets a link's price by hand, with its currency", async () => {
    const user = userEvent.setup()
    const state = { item: withLinks(other) }
    const fetch = itemServer(state, {
      'PATCH /api/v2/items/21/store-links/32': () => {
        const updated = { ...other, price_minor: 8950, currency: 'EUR', captured_at: '2026-11-08T13:00:00Z' }
        state.item = withLinks(updated)
        return { status: 200, body: updated }
      },
    })
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Edit price of other.example' }))

    await user.clear(screen.getByLabelText('Currency'))
    await user.type(screen.getByLabelText('Currency'), 'eur')
    await user.type(screen.getByLabelText('Price'), '89.50')
    await user.click(screen.getByRole('button', { name: 'Save price' }))

    expect(await screen.findByText('€89.50')).toBeInTheDocument()
    expect(sentBody(fetch, 'PATCH /api/v2/items/21/store-links/32')).toEqual({ price_minor: 8950, currency: 'EUR' })
    expect(screen.queryByLabelText('Price')).not.toBeInTheDocument()
  })

  test('clears the price when the field is left empty', async () => {
    const user = userEvent.setup()
    const state = { item: withLinks(shop) }
    const fetch = itemServer(state, {
      'PATCH /api/v2/items/21/store-links/31': () => {
        state.item = withLinks({ ...shop, price_minor: null, currency: null })
        return { status: 200, body: state.item.store_links[0] }
      },
    })
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Edit price of shop.example' }))
    expect(screen.getByLabelText('Price')).toHaveValue('49.99')

    await user.clear(screen.getByLabelText('Price'))
    await user.click(screen.getByRole('button', { name: 'Save price' }))

    await waitFor(() => expect(screen.queryByText('$49.99')).not.toBeInTheDocument())
    expect(sentBody(fetch, 'PATCH /api/v2/items/21/store-links/31')).toEqual({ price_minor: null })
  })

  test('refuses a price that is not a price, before the server is asked', async () => {
    const user = userEvent.setup()
    const fetch = itemServer({ item: withLinks(shop) })
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Edit price of shop.example' }))
    await user.clear(screen.getByLabelText('Price'))
    await user.type(screen.getByLabelText('Price'), 'cheap')
    await user.click(screen.getByRole('button', { name: 'Save price' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/price/i)
    expect(fetch.mock.calls.some(([, init]) => init?.method === 'PATCH')).toBe(false)
  })

  test('removes a link', async () => {
    const user = userEvent.setup()
    const state = { item: withLinks(shop, other) }
    const fetch = itemServer(state, {
      'DELETE /api/v2/items/21/store-links/31': () => {
        state.item = withLinks(other)
        return { status: 204 }
      },
    })
    open('/items/21')
    await user.click(await screen.findByRole('button', { name: 'Remove link shop.example' }))

    await waitFor(() => expect(screen.queryByRole('link', { name: 'shop.example' })).not.toBeInTheDocument())
    expect(screen.getByRole('link', { name: 'other.example' })).toBeInTheDocument()
    expect(fetch.mock.calls.some(([input, init]) => init?.method === 'DELETE' && String(input) === '/api/v2/items/21/store-links/31')).toBe(true)
  })
})

describe('refreshing prices', () => {
  test('refreshes, shows the new prices, and says how many changed', async () => {
    const user = userEvent.setup()
    const state = { item: withLinks(shop, link('32', 'other.example', { price_minor: 2000, currency: 'USD', captured_at: '2026-11-08T12:00:00Z' })) }
    itemServer(state, {
      'POST /api/v2/items/21/refresh-prices': () => {
        state.item = withLinks({ ...shop, price_minor: 4500, captured_at: '2026-11-09T09:00:00Z' }, state.item.store_links[1]!)
        return { status: 200, body: state.item }
      },
    })
    open('/items/21')

    await user.click(await screen.findByRole('button', { name: 'Refresh prices' }))

    expect(await screen.findByText('$45.00')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('1 of 2 prices changed.')
  })

  test('counts a changed price even when the captured date is the same second as before', async () => {
    const user = userEvent.setup()
    const state = { item: withLinks(shop) }
    itemServer(state, {
      'POST /api/v2/items/21/refresh-prices': () => {
        state.item = withLinks({ ...shop, price_minor: 3900 })
        return { status: 200, body: state.item }
      },
    })
    open('/items/21')

    await user.click(await screen.findByRole('button', { name: 'Refresh prices' }))

    expect(await screen.findByRole('status')).toHaveTextContent('1 of 1 price changed.')
  })

  test('says so when no price changed, and when the hourly limit stopped it', async () => {
    const user = userEvent.setup()
    let answer: { status: number; body: unknown } = { status: 200, body: withLinks(shop) }
    itemServer({ item: withLinks(shop) }, { 'POST /api/v2/items/21/refresh-prices': () => answer })
    open('/items/21')

    await user.click(await screen.findByRole('button', { name: 'Refresh prices' }))
    expect(await screen.findByRole('status')).toHaveTextContent('No price changed.')

    answer = problem(429, 'rate_limited', 'You have looked up a lot of links and photos. Try again later.')
    await user.click(screen.getByRole('button', { name: 'Refresh prices' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Try again later')
  })

  test('has no Refresh prices without a store link', async () => {
    itemServer({ item: kettle })
    open('/items/21')

    await screen.findByRole('button', { name: 'Add link' })
    expect(screen.queryByRole('button', { name: 'Refresh prices' })).not.toBeInTheDocument()
  })
})
