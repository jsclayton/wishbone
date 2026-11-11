import { cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ImageSummary, Item } from './api'
import { ada, adaList, heading, kettle, ME, open, page, problem, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const image = (id: string): ImageSummary => ({ id, url: `/api/v2/images/${id}`, width: 40, height: 30 })
const withImages = (ids: string[], primary = ids[0]): Item => ({
  ...kettle,
  images: ids.map(image),
  primary_image: primary ? image(primary) : null,
})
/** What the item's page loads besides the item. */
const itemPageRoutes = { 'GET /api/v2/lists/11': { status: 200, body: adaList }, 'GET /api/v2/users': page([ada]) }
const upload = (id: string) => ({ status: 201, body: { ...image(id), content_type: 'image/jpeg' } })

/** The item's routes, with the item held in `state`. A PATCH of the images changes it as the server would. */
function imagesServer(state: { item: Item; version?: number }, more: Parameters<typeof serve>[0] = {}) {
  state.version ??= 1
  const tag = () => ({ ETag: `"e${state.version}"` })
  return serve({
    [ME]: { status: 200, body: ada },
    'GET /api/v2/items/21': () => ({ status: 200, body: state.item, headers: tag() }),
    'PATCH /api/v2/items/21': (init) => {
      const changes = JSON.parse(String(init?.body)) as { image_ids?: string[]; primary_image_id?: string | null }
      const ids = changes.image_ids ?? state.item.images.map((i) => i.id)
      let primary = changes.primary_image_id === undefined ? state.item.primary_image?.id : (changes.primary_image_id ?? ids[0])
      if (!ids.includes(primary ?? '')) primary = ids[0]
      state.item = withImages(ids, primary)
      state.version = (state.version ?? 1) + 1
      return { status: 200, body: state.item, headers: tag() }
    },
    ...more,
  })
}

const ifMatches = (fetch: ReturnType<typeof serve>) =>
  fetch.mock.calls.filter(([, init]) => init?.method === 'PATCH').map(([, init]) => (init!.headers as Record<string, string>)['If-Match'])

const patches = (fetch: ReturnType<typeof serve>) =>
  fetch.mock.calls.filter(([, init]) => init?.method === 'PATCH').map(([, init]) => JSON.parse(String(init?.body)))

describe("an item's images screen", () => {
  test('shows the images in the order they were added, marks the primary image, and has no way to reorder', async () => {
    imagesServer({ item: withImages(['1', '2', '3'], '2') })
    open('/items/21/images')

    expect(await heading('Images')).toBeInTheDocument()
    const list = screen.getByRole('list', { name: 'Images' })
    expect(within(list).getAllByRole('img').map((i) => i.getAttribute('src'))).toEqual(['/api/v2/images/1', '/api/v2/images/2', '/api/v2/images/3'])
    expect(screen.getByRole('img', { name: 'A kettle: 2 of 3, primary' })).toBeInTheDocument()
    expect(screen.getByText('Primary image')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /^Make image \d the primary image$/ })).toHaveLength(2)
    expect(screen.queryByRole('button', { name: /reorder|move up|move down/i })).not.toBeInTheDocument()
  })

  test('Make primary sends primary_image_id and keeps the order', async () => {
    const user = userEvent.setup()
    const fetch = imagesServer({ item: withImages(['1', '2', '3']) })
    open('/items/21/images')
    await user.click(await screen.findByRole('button', { name: 'Make image 3 the primary image' }))

    expect(await screen.findByRole('img', { name: 'A kettle: 3 of 3, primary' })).toBeInTheDocument()
    expect(patches(fetch)).toEqual([{ primary_image_id: '3' }])
    expect(within(screen.getByRole('list', { name: 'Images' })).getAllByRole('img').map((i) => i.getAttribute('src'))).toEqual([
      '/api/v2/images/1',
      '/api/v2/images/2',
      '/api/v2/images/3',
    ])
  })

  test('Remove sends the remaining image_ids, and the first remaining image becomes primary', async () => {
    const user = userEvent.setup()
    const fetch = imagesServer({ item: withImages(['1', '2', '3']) })
    open('/items/21/images')
    await user.click(await screen.findByRole('button', { name: 'Remove image 1' }))

    await waitFor(() => expect(screen.getAllByRole('img')).toHaveLength(2))
    expect(patches(fetch)).toEqual([{ image_ids: ['2', '3'] }])
    expect(screen.getByRole('img', { name: 'A kettle: 1 of 2, primary' })).toHaveAttribute('src', '/api/v2/images/2')
  })

  test('Add an image uploads the file, then sends the images with the new one last', async () => {
    const user = userEvent.setup()
    const fetch = imagesServer({ item: withImages(['1']) }, { 'POST /api/v2/images': upload('9') })
    open('/items/21/images')
    await user.click(await screen.findByRole('button', { name: 'Add an image' }))
    await user.upload(screen.getByLabelText('Choose an image to add'), new File(['x'], 'k.jpg', { type: 'image/jpeg' }))

    await waitFor(() => expect(screen.getAllByRole('img')).toHaveLength(2))
    expect(patches(fetch)).toEqual([{ image_ids: ['1', '9'] }])
  })

  test('says why a file was refused, and why an image could not be attached', async () => {
    const user = userEvent.setup()
    let uploaded: { status: number; body: unknown } = problem(422, 'image_invalid', 'That file is not an image.')
    let refused: { status: number; body: unknown } | null = null
    imagesServer(
      { item: withImages(['1']) },
      { 'POST /api/v2/images': () => uploaded, 'PATCH /api/v2/items/21': () => refused ?? { status: 200, body: withImages(['1']) } },
    )
    open('/items/21/images')
    await user.click(await screen.findByRole('button', { name: 'Add an image' }))
    await user.upload(screen.getByLabelText('Choose an image to add'), new File(['x'], 'k.txt', { type: 'image/jpeg' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('That file is not an image.')
    expect(screen.getAllByRole('img')).toHaveLength(1)

    uploaded = upload('9')
    refused = problem(422, 'too_many_images', 'Too many.')
    await user.upload(screen.getByLabelText('Choose an image to add'), new File(['x'], 'k.jpg', { type: 'image/jpeg' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Too many.')
  })

  test('sends If-Match with the version it loaded, and then with the version of the last answer', async () => {
    const user = userEvent.setup()
    const fetch = imagesServer({ item: withImages(['1', '2', '3']) })
    open('/items/21/images')
    await user.click(await screen.findByRole('button', { name: 'Make image 3 the primary image' }))
    await screen.findByRole('img', { name: 'A kettle: 3 of 3, primary' })
    await user.click(screen.getByRole('button', { name: 'Remove image 1' }))
    await waitFor(() => expect(screen.getAllByRole('img')).toHaveLength(2))

    expect(ifMatches(fetch)).toEqual(['"e1"', '"e2"'])
  })

  test('on a 412 loads the item again, says it changed, and changes nothing', async () => {
    const user = userEvent.setup()
    const state: { item: Item; version?: number } = { item: withImages(['1', '2']) }
    let stale = true
    const fetch = imagesServer(state, {
      'PATCH /api/v2/items/21': () => {
        if (stale) {
          // Another editor added an image meanwhile.
          state.item = withImages(['1', '2', '7'])
          state.version = 5
          return problem(412, 'resource_changed')
        }
        return { status: 200, body: state.item, headers: { ETag: '"e6"' } }
      },
    })
    open('/items/21/images')
    await user.click(await screen.findByRole('button', { name: 'Remove image 1' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('This item changed while you were looking')
    expect(screen.getAllByRole('img')).toHaveLength(3)
    expect(screen.getByRole('img', { name: 'A kettle: 3 of 3' })).toHaveAttribute('src', '/api/v2/images/7')

    // The next change goes with the version it loaded again.
    stale = false
    await user.click(screen.getByRole('button', { name: 'Remove image 1' }))
    await waitFor(() => expect(ifMatches(fetch)).toEqual(['"e1"', '"e5"']))
  })

  test('has no way to add at five images', async () => {
    imagesServer({ item: withImages(['1', '2', '3', '4', '5']) })
    open('/items/21/images')

    await heading('Images')
    expect(screen.queryByRole('button', { name: 'Add an image' })).not.toBeInTheDocument()
    expect(screen.getByText(/at most 5|can have 5 images/)).toBeInTheDocument()
  })

  test('shows the missing-page message to a user who cannot edit the list', async () => {
    imagesServer({ item: { ...withImages(['1']), can_edit: false, can_delete: false } })
    open('/items/21/images')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })

  test('Done goes back to the item', async () => {
    const user = userEvent.setup()
    imagesServer({ item: withImages(['1']) }, itemPageRoutes)
    open('/items/21/images')
    await user.click(await screen.findByRole('button', { name: 'Done' }))

    expect(window.location.pathname).toBe('/items/21')
  })

  test("the item page links editors to it, with the count, and to Add an image when there are none", async () => {
    const state = { item: withImages(['1', '2']) }
    imagesServer(state, itemPageRoutes)
    open('/items/21')

    expect(await screen.findByRole('link', { name: 'Change images (2)' })).toHaveAttribute('href', '/items/21/images')
    state.item = kettle
    cleanup()
    open('/items/21')
    expect(await screen.findByRole('link', { name: 'Add an image' })).toHaveAttribute('href', '/items/21/images')
  })
})
