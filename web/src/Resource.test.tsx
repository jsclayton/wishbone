import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Resource, SignedOutContext } from './Resource'

type Item = { id: string; title: string }

function serve(status: number, body: unknown = {}) {
  const fetch = vi.fn<typeof globalThis.fetch>(async () => new Response(JSON.stringify(body), { status }))
  vi.stubGlobal('fetch', fetch)
  return fetch
}

function show(path = '/api/v2/items/9') {
  return render(<Resource<Item> path={path}>{(item) => <h1>{item.title}</h1>}</Resource>)
}

afterEach(() => vi.unstubAllGlobals())

test('shows the screen once the resource is in', async () => {
  const fetch = serve(200, { id: '9', title: 'Blue mug' })
  show()
  expect(await screen.findByRole('heading', { name: 'Blue mug' })).toBeInTheDocument()
  expect(fetch).toHaveBeenCalledWith('/api/v2/items/9', expect.anything())
})

test('shows nothing while the resource loads', () => {
  vi.stubGlobal('fetch', () => new Promise(() => {}))
  show()
  expect(document.body).toHaveTextContent('')
})

test('a missing resource shows the missing-page message', async () => {
  serve(404, { status: 404, code: 'not_found', detail: 'There is nothing at this address.' })
  show()
  expect(await screen.findByRole('heading', { level: 1, name: "We can't find that page" })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Go to Home' })).toHaveAttribute('href', '/')
})

test('a resource that is hidden shows the same message as one that is gone', async () => {
  // The API answers 404 for both (docs/spec.md, Errors), so the screen can't tell them apart.
  serve(404, { status: 404, code: 'not_found', detail: 'x' })
  const { unmount } = show()
  const gone = (await screen.findByRole('heading', { level: 1 })).textContent
  unmount()
  serve(404, { status: 404, code: 'not_found', detail: 'y' })
  show()
  expect((await screen.findByRole('heading', { level: 1 })).textContent).toBe(gone)
})

test('a server that fails says Wishbone cannot be reached, and Try again asks again', async () => {
  const user = userEvent.setup()
  const fetch = serve(503)
  show()
  expect(await screen.findByRole('heading', { name: "Can't reach Wishbone" })).toBeInTheDocument()

  fetch.mockResolvedValue(new Response(JSON.stringify({ id: '9', title: 'Blue mug' }), { status: 200 }))
  await user.click(screen.getByRole('button', { name: 'Try again' }))

  expect(await screen.findByRole('heading', { name: 'Blue mug' })).toBeInTheDocument()
})

test('a new path loads the new resource', async () => {
  const fetch = serve(200, { id: '9', title: 'Blue mug' })
  const { rerender } = show()
  await screen.findByRole('heading', { name: 'Blue mug' })

  fetch.mockResolvedValue(new Response(JSON.stringify({ id: '10', title: 'Red mug' }), { status: 200 }))
  rerender(<Resource<Item> path="/api/v2/items/10">{(item) => <h1>{item.title}</h1>}</Resource>)

  expect(await screen.findByRole('heading', { name: 'Red mug' })).toBeInTheDocument()
})

test('a session that ended sends the app to the signed-out page, not to "Can\'t reach Wishbone"', async () => {
  serve(401, { status: 401, code: 'unauthorized', detail: 'Sign in to do this.' })
  const onSignedOut = vi.fn<() => void>()
  render(
    <SignedOutContext value={onSignedOut}>
      <Resource<Item> path="/api/v2/items/9">{(item) => <h1>{item.title}</h1>}</Resource>
    </SignedOutContext>,
  )

  await waitFor(() => expect(onSignedOut).toHaveBeenCalledTimes(1))
  expect(screen.queryByRole('heading', { name: "Can't reach Wishbone" })).not.toBeInTheDocument()
  expect(document.body).toHaveTextContent('')
})
