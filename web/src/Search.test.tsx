import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { SearchResult } from './api'
import { ada, adaList, heading, kettle, ME, open, page, problem, serve, sentBody } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const asAda = { [ME]: { status: 200, body: ada }, 'GET /api/v2/groups?includes_me=true': page([]) }

const nothing: SearchResult = {
  title: null,
  notes: null,
  notes_cut: false,
  price_minor: null,
  top_price_minor: null,
  currency: null,
  store_links: [],
  image_urls: [],
  options: [],
  found: false,
}
const words = (text: string, more: Partial<SearchResult> = {}): SearchResult => ({ ...nothing, title: text, notes: text, ...more })
const found: SearchResult = {
  ...nothing,
  title: 'Copper kettle',
  found: true,
  store_links: [{ url: 'https://shop.example/kettle', store: 'shop.example', price_minor: 4999, currency: 'USD', image_url: 'https://cdn.shop.example/a.jpg' }],
  image_urls: ['https://cdn.shop.example/a.jpg', 'https://cdn.shop.example/b.jpg', 'https://cdn.shop.example/c.jpg'],
}
const searched = (result: SearchResult) => ({ status: 200, body: { results: [result] } })
const list = { 'GET /api/v2/lists/11': { status: 200, body: { ...adaList, item_count: 1 } }, 'GET /api/v2/lists/11/items': page([kettle]), 'GET /api/v2/users': page([ada]) }
const saved = { status: 201, body: kettle }
const upload = (id: string) => ({ status: 201, body: { id, url: `/api/v2/images/${id}`, width: 40, height: 30, content_type: 'image/jpeg' } })

const headersOf = (init?: RequestInit) => (init?.headers ?? {}) as Record<string, string>

/** What a search sent: the form's text, or whether it had a file. */
function sentForm(fetch: ReturnType<typeof serve>) {
  const call = fetch.mock.calls.find(([input, init]) => init?.method === 'POST' && String(input) === '/api/v2/search')
  if (!call) throw new Error('POST /api/v2/search was not called')
  const [, init] = call
  const text = new TextDecoder().decode(init?.body as ArrayBuffer)
  const typed = /name="text"\r\n\r\n([^\r]*)\r\n/.exec(text)
  return {
    contentType: headersOf(init)['Content-Type']!,
    text: typed ? typed[1]! : null,
    hasImage: /name="image"; filename="photo"/.test(text),
  }
}
const calls = (fetch: ReturnType<typeof serve>, key: string) =>
  fetch.mock.calls.filter(([input, init]) => `${init?.method ?? 'GET'} ${String(input)}` === key)

describe('the paste box on Home', () => {
  test('sends typed words to the search, then opens the add form with the words as the title and the notes', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...asAda, 'POST /api/v2/search': searched(words('a book about sharks')), 'POST /api/v2/lists/11/items': saved, ...list })
    open('/')
    await heading('Home')

    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), '  a book about sharks ')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await heading('Add an item')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/lists/11/add')
    expect(sentForm(fetch)).toMatchObject({ text: 'a book about sharks', hasImage: false })
    expect(sentForm(fetch).contentType).toMatch(/^multipart\/form-data; boundary=\S+$/)
    expect(screen.getByLabelText('Title')).toHaveValue('a book about sharks')
    expect(screen.getByLabelText('Notes')).toHaveValue('a book about sharks')

    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(await heading('Ada')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toEqual({ title: 'a book about sharks', notes: 'a book about sharks', price_minor: null, top_price_minor: null, quantity: 1, is_unlimited: false })
    expect(window.location.pathname).toBe('/lists/11')
  })

  test('does nothing for empty words', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...asAda })
    open('/')
    await heading('Home')

    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(fetch).not.toHaveBeenCalledWith('/api/v2/search', expect.anything())
  })

  test('a link from a blocked store stays in the box with the words of the server', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, 'POST /api/v2/search': problem(422, 'store_blocked', "Links from temu.com can't be added.") })
    open('/')
    await heading('Home')

    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://temu.com/p/1')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await screen.findByRole('alert')).toHaveTextContent("Links from temu.com can't be added.")
    expect(screen.getByRole('textbox', { name: 'Add to my list' })).toHaveValue('https://temu.com/p/1')
    expect(window.location.pathname).toBe('/')
  })

  test('another refusal and a failure also keep the box and say so', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, 'POST /api/v2/search': problem(422, 'invalid_input', 'Enter a web address that starts with http or https.') })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'http://[[::1')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter a web address that starts with http or https.')

    serve({ ...asAda, 'POST /api/v2/search': problem(500, 'internal_error') })
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(screen.getByRole('textbox', { name: 'Add to my list' })).toHaveValue('http://[::1')
  })
})

describe('the add form that a link opens', () => {
  test('fills in the title, the price and the store, and saves the store link, the picked images, and the first as the primary image', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'POST /api/v2/search': searched(found),
      'POST /api/v2/images': (init) => upload(JSON.parse(String(init?.body)).url.endsWith('a.jpg') ? '41' : '43'),
      'POST /api/v2/lists/11/items': saved,
      ...list,
    })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle?utm_source=x')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await heading('Add an item')).toBeInTheDocument()
    expect(screen.getByLabelText('Title')).toHaveValue('Copper kettle')
    expect(screen.getByLabelText('Price')).toHaveValue('49.99')
    expect(screen.getByLabelText('Currency')).toHaveValue('USD')
    expect(screen.getByText(/We filled this in from your link/)).toBeInTheDocument()
    expect(screen.getByText('shop.example')).toBeInTheDocument()
    const pictures = screen.getAllByRole('checkbox', { name: /Use image/ })
    expect(pictures).toHaveLength(3)
    expect(pictures.every((p) => (p as HTMLInputElement).checked)).toBe(true)

    await user.click(pictures[1]!)
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await heading('Ada')).toBeInTheDocument()
    expect(calls(fetch, 'POST /api/v2/images').map(([, init]) => JSON.parse(String(init?.body)))).toEqual([
      { url: 'https://cdn.shop.example/a.jpg' },
      { url: 'https://cdn.shop.example/c.jpg' },
    ])
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toEqual({
      title: 'Copper kettle',
      price_minor: 4999,
      top_price_minor: null,
      currency: 'USD',
      quantity: 1,
      is_unlimited: false,
      store_links: [{ url: 'https://shop.example/kettle', price_minor: 4999, currency: 'USD', image_url: 'https://cdn.shop.example/a.jpg' }],
      image_ids: ['41', '43'],
      primary_image_id: '41',
    })
  })

  test('leaves out an image that cannot be downloaded, and still adds the item', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'POST /api/v2/search': searched({ ...found, image_urls: ['https://cdn.shop.example/a.jpg', 'https://cdn.shop.example/b.jpg'] }),
      'POST /api/v2/images': (init) =>
        JSON.parse(String(init?.body)).url.endsWith('a.jpg') ? problem(422, 'image_fetch_failed', "That image couldn't be downloaded.") : upload('43'),
      'POST /api/v2/lists/11/items': saved,
      ...list,
    })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Add an item')

    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await heading('Ada')).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toMatchObject({ image_ids: ['43'], primary_image_id: '43' })
  })

  test('sends no images when none is picked, and a refusal of the item keeps the form and reuses the images it saved', async () => {
    const user = userEvent.setup()
    let refuse = true
    const fetch = serve({
      ...asAda,
      'POST /api/v2/search': searched({ ...found, image_urls: ['https://cdn.shop.example/a.jpg'] }),
      'POST /api/v2/images': upload('41'),
      'POST /api/v2/lists/11/items': () => (refuse ? problem(422, 'store_blocked', "Links from shop.example can't be added.") : saved),
      ...list,
    })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Add an item')

    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(await screen.findByRole('alert')).toHaveTextContent("Links from shop.example can't be added.")
    expect(screen.getByLabelText('Title')).toHaveValue('Copper kettle')

    refuse = false
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(await heading('Ada')).toBeInTheDocument()
    expect(calls(fetch, 'POST /api/v2/images')).toHaveLength(1)
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toMatchObject({ image_ids: ['41'] })
  })

  test('says it could not read a link that gave nothing, and keeps the link as the store link', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'POST /api/v2/search': searched({ ...nothing, store_links: [{ url: 'https://shop.example/x', store: 'shop.example', price_minor: null, currency: null, image_url: null }] }),
      'POST /api/v2/lists/11/items': saved,
      ...list,
    })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'shop.example/x')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await heading('Add an item')).toBeInTheDocument()
    expect(screen.getByText(/We couldn't read that link/)).toBeInTheDocument()
    expect(screen.getByLabelText('Title')).toHaveValue('')
    await user.type(screen.getByLabelText('Title'), 'Kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    await heading('Ada')
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toMatchObject({ title: 'Kettle', store_links: [{ url: 'https://shop.example/x' }] })
  })

  test('over the hourly limit the form keeps the link, the other fields are blank, and a notice says it was not read', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'POST /api/v2/search': problem(429, 'rate_limited', 'Try again later.'),
      'POST /api/v2/lists/11/items': saved,
      ...list,
    })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await heading('Add an item')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent("We didn't read this one")
    expect(screen.getByLabelText('Title')).toHaveValue('')
    expect(screen.getByLabelText('Price')).toHaveValue('')
    expect(screen.getByText('shop.example')).toBeInTheDocument()

    await user.type(screen.getByLabelText('Title'), 'Kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Ada')
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toMatchObject({ title: 'Kettle', store_links: [{ url: 'https://shop.example/kettle' }] })
  })

  test('Fill it in myself stops the wait and opens the form with the link', async () => {
    const user = userEvent.setup()
    // A search that never answers, until the user stops waiting.
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string, init?: RequestInit) => {
        if (String(input) === '/api/v2/users/me') return new Response(JSON.stringify(ada), { status: 200 })
        if (String(input) === '/api/v2/groups?includes_me=true') return new Response(JSON.stringify({ data: [], next_cursor: null, total_count: 0 }), { status: 200 })
        if (String(input) === '/api/v2/lists/11') return new Response(JSON.stringify(adaList), { status: 200 })
        return new Promise<Response>((_resolve, reject) => init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError'))))
      }),
    )
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    await user.click(await screen.findByRole('button', { name: 'Fill it in myself' }))

    expect(await heading('Add an item')).toBeInTheDocument()
    expect(screen.getByText('shop.example')).toBeInTheDocument()
  })
})

describe('the options a page offers', () => {
  const withOptions: SearchResult = {
    ...found,
    image_urls: [],
    options: [
      { name: 'Size', values: ['S', 'M', 'L'], selected: 'M' },
      { name: 'Color', values: ['Black', 'Navy'], selected: null },
      { name: 'Fit', values: [], selected: null },
    ],
  }

  test('each option with values is a dropdown, preset to the page\'s pick or Not sure, and the choices are saved into the notes', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...asAda, 'POST /api/v2/search': searched(withOptions), 'POST /api/v2/lists/11/items': saved, ...list })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Add an item')

    expect(screen.getByLabelText('Size')).toHaveValue('M')
    expect(screen.getByLabelText('Color')).toHaveValue('')
    expect(screen.getAllByRole('option', { name: 'Not sure' })).toHaveLength(2)
    expect(screen.queryByLabelText('Fit')).not.toBeInTheDocument()
    // An option with no values starts a line of the notes.
    expect(screen.getByLabelText('Notes')).toHaveValue('Fit:')

    await user.selectOptions(screen.getByLabelText('Color'), 'Navy')
    await user.type(screen.getByLabelText('Notes'), ' wide')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    await heading('Ada')
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toMatchObject({ notes: 'Size: M\nColor: Navy\nFit: wide' })
  })

  test('Not sure and an unchanged header line write nothing', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...asAda, 'POST /api/v2/search': searched(withOptions), 'POST /api/v2/lists/11/items': saved, ...list })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Add an item')

    await user.selectOptions(screen.getByLabelText('Size'), 'Not sure')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    await heading('Ada')
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).not.toHaveProperty('notes')
  })

  test('says how many characters to take out when the choices make the notes too long', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, 'POST /api/v2/search': searched(withOptions), ...list })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Add an item')

    await user.clear(screen.getByLabelText('Notes'))
    await user.click(screen.getByLabelText('Notes'))
    await user.paste('x'.repeat(1995))
    await user.click(screen.getByRole('button', { name: 'Add' }))

    // "Size: M" is 7 characters and a line break: 2003 in all.
    expect(await screen.findByRole('alert')).toHaveTextContent('Take out 3 characters')
    expect(screen.getByLabelText('Notes')).toHaveValue('x'.repeat(1995))
  })
})

describe('the add form from a search', () => {
  test('a price the user clears is cleared again after the add, because the server gives the item the store link\'s', async () => {
    const user = userEvent.setup()
    const priced = { ...kettle, price_minor: 4999, currency: 'USD' }
    const fetch = serve({
      ...asAda,
      'POST /api/v2/search': searched({ ...found, image_urls: [] }),
      'POST /api/v2/lists/11/items': { status: 201, body: priced },
      'PATCH /api/v2/items/21': { status: 200, body: kettle },
      ...list,
    })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Add an item')

    await user.clear(screen.getByLabelText('Price'))
    await user.click(screen.getByRole('button', { name: 'Add' }))

    await heading('Ada')
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toMatchObject({
      price_minor: null,
      top_price_minor: null,
      store_links: [{ url: 'https://shop.example/kettle', price_minor: 4999, currency: 'USD' }],
    })
    expect(sentBody(fetch, 'PATCH /api/v2/items/21')).toEqual({ price_minor: null })
  })

  test.each([
    ['refuses it', problem(422, 'invalid_input', 'No.')],
    ['fails', problem(500, 'internal_error')],
  ])('says so when the server %s to clear the price, and does not add the item again', async (_, patchAnswer) => {
    const user = userEvent.setup()
    const priced = { ...kettle, price_minor: 4999, currency: 'USD' }
    const fetch = serve({
      ...asAda,
      'POST /api/v2/search': searched({ ...found, image_urls: [] }),
      'POST /api/v2/lists/11/items': { status: 201, body: priced },
      'PATCH /api/v2/items/21': patchAnswer,
      ...list,
    })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Add an item')
    await user.clear(screen.getByLabelText('Price'))
    await user.click(screen.getByRole('button', { name: 'Add' }))

    expect(await screen.findByRole('alert')).toHaveTextContent("A kettle was added, but we couldn't remove its price")
    expect(screen.getByRole('link', { name: 'Open A kettle' })).toHaveAttribute('href', '/items/21')
    expect(screen.getByRole('link', { name: 'Back to the list' })).toHaveAttribute('href', '/lists/11')
    expect(calls(fetch, 'POST /api/v2/lists/11/items')).toHaveLength(1)
  })

  test('a price the user keeps is not cleared', async () => {
    const user = userEvent.setup()
    const priced = { ...kettle, price_minor: 4999, currency: 'USD' }
    const fetch = serve({
      ...asAda,
      'POST /api/v2/search': searched({ ...found, image_urls: [] }),
      'POST /api/v2/lists/11/items': { status: 201, body: priced },
      ...list,
    })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Add an item')

    await user.click(screen.getByRole('button', { name: 'Add' }))

    await heading('Ada')
    expect(calls(fetch, 'PATCH /api/v2/items/21')).toHaveLength(0)
  })

  test('Primary image marks the image that will be the primary image: the first one picked', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, 'POST /api/v2/search': searched(found), ...list })
    open('/')
    await heading('Home')
    await user.type(screen.getByRole('textbox', { name: 'Add to my list' }), 'https://shop.example/kettle')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Add an item')
    const mainOf = () =>
      screen
        .getAllByRole('checkbox', { name: /Use image/ })
        .filter((box) => box.closest('label')?.textContent?.includes('Primary image'))
        .map((box) => box.getAttribute('aria-label'))

    expect(mainOf()).toEqual(['Use image 1'])
    await user.click(screen.getByRole('checkbox', { name: 'Use image 1' }))
    expect(mainOf()).toEqual(['Use image 2'])
    await user.click(screen.getByRole('checkbox', { name: 'Use image 2' }))
    expect(mainOf()).toEqual(['Use image 3'])
    await user.click(screen.getByRole('checkbox', { name: 'Use image 3' }))
    expect(mainOf()).toEqual([])
  })

  test('Fill it in myself cuts typed words to the longest title, as the search does', async () => {
    const user = userEvent.setup()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: string, init?: RequestInit) => {
        if (String(input) === '/api/v2/users/me') return new Response(JSON.stringify(ada), { status: 200 })
        if (String(input) === '/api/v2/groups?includes_me=true') return new Response(JSON.stringify({ data: [], next_cursor: null, total_count: 0 }), { status: 200 })
        if (String(input) === '/api/v2/lists/11') return new Response(JSON.stringify(adaList), { status: 200 })
        return new Promise<Response>((_resolve, reject) => init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError'))))
      }),
    )
    open('/')
    await heading('Home')
    await user.click(screen.getByRole('textbox', { name: 'Add to my list' }))
    await user.paste('é'.repeat(2500))
    await user.click(screen.getByRole('button', { name: 'Add' }))

    await user.click(await screen.findByRole('button', { name: 'Fill it in myself' }))

    await heading('Add an item')
    expect([...(screen.getByLabelText('Title') as HTMLInputElement).value]).toHaveLength(300)
    expect([...(screen.getByLabelText('Notes') as HTMLTextAreaElement).value]).toHaveLength(2000)
    expect(screen.getByText(/we kept the first 2000 characters/)).toBeInTheDocument()
  })
})

describe('the camera button', () => {
  const photo = () => new File([new Uint8Array([1, 2, 3])], 'cat.png', { type: 'image/png' })

  test('sends the photo to the search, and the form has the guess; saving uploads the photo and then adds the item', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'POST /api/v2/search': searched({ ...nothing, title: 'Blue mug', price_minor: 1250, currency: 'EUR', found: true }),
      'POST /api/v2/images': upload('51'),
      'POST /api/v2/lists/11/items': saved,
      ...list,
    })
    open('/')
    await heading('Home')

    expect(screen.getByRole('button', { name: 'Take or choose a photo' })).toBeInTheDocument()
    await user.upload(screen.getByLabelText('Photo file'), photo())

    expect(await heading('Add an item')).toBeInTheDocument()
    expect(sentForm(fetch)).toMatchObject({ hasImage: true, text: null })
    expect(screen.getByLabelText('Title')).toHaveValue('Blue mug')
    expect(screen.getByLabelText('Price')).toHaveValue('12.50')
    expect(screen.getByLabelText('Currency')).toHaveValue('EUR')
    expect(screen.getByRole('img', { name: 'What you picked' })).toHaveAttribute('src', expect.stringMatching(/^data:/))

    await user.click(screen.getByRole('button', { name: 'Add' }))

    await heading('Ada')
    const uploadCall = calls(fetch, 'POST /api/v2/images')[0]!
    expect(headersOf(uploadCall[1])['Content-Type']).toBe('image/png')
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toMatchObject({ title: 'Blue mug', image_ids: ['51'], primary_image_id: '51' })
    const order = fetch.mock.calls.map(([input, init]) => `${init?.method ?? 'GET'} ${String(input)}`)
    expect(order.indexOf('POST /api/v2/images')).toBeLessThan(order.indexOf('POST /api/v2/lists/11/items'))
  })

  test('a photo with no title in the result gets "Photo from {date}"', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, 'POST /api/v2/search': searched(nothing), ...list })
    open('/')
    await heading('Home')

    await user.upload(screen.getByLabelText('Photo file'), photo())

    await heading('Add an item')
    expect(screen.getByLabelText('Title')).toHaveValue(`Photo from ${new Date().toLocaleDateString('en-US', { month: 'short', day: 'numeric' })}`)
  })

  test('over the hourly limit a photo still opens the form, with its title, the photo, and a notice', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...asAda,
      'POST /api/v2/search': problem(429, 'rate_limited'),
      'POST /api/v2/images': upload('51'),
      'POST /api/v2/lists/11/items': saved,
      ...list,
    })
    open('/')
    await heading('Home')

    await user.upload(screen.getByLabelText('Photo file'), photo())

    await heading('Add an item')
    expect((screen.getByLabelText('Title') as HTMLInputElement).value).toMatch(/^Photo from /)
    expect(screen.getByRole('status')).toHaveTextContent("We didn't read this one")
    expect(screen.getByRole('img', { name: 'What you picked' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await heading('Ada')
    expect(sentBody(fetch, 'POST /api/v2/lists/11/items')).toMatchObject({ image_ids: ['51'] })
  })

  test('a file the server refuses shows its words and stays on Home', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, 'POST /api/v2/search': problem(422, 'image_invalid', "That file isn't an image we can read.") })
    open('/')
    await heading('Home')

    await user.upload(screen.getByLabelText('Photo file'), photo())

    expect(await screen.findByRole('alert')).toHaveTextContent("That file isn't an image we can read.")
    expect(window.location.pathname).toBe('/')
  })
})
