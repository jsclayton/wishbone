import { render, screen, waitFor } from '@testing-library/react'
import { Resource, SignedOutContext } from './Resource'

type Row = { id: string }
type Rows = { data: Row[]; next_cursor: string | null; total_count: number }

const rows = (ids: string[], next: string | null): Rows => ({ data: ids.map((id) => ({ id })), next_cursor: next, total_count: 3 })
const reply = (status: number, body?: unknown) => new Response(body === undefined ? null : JSON.stringify(body), { status })

afterEach(() => vi.unstubAllGlobals())

// A client always follows next_cursor (docs/spec.md, API principles, Collections).
describe('a collection with all', () => {
  test('follows next_cursor to the last page and shows every row', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async (input) => {
      const url = String(input)
      if (url.endsWith('cursor=b')) return reply(200, rows(['3'], null))
      if (url.endsWith('cursor=a')) return reply(200, rows(['2'], 'b'))
      return reply(200, rows(['1'], 'a'))
    })
    vi.stubGlobal('fetch', fetch)
    render(
      <Resource<Rows> path="/api/v2/users?sort=name" all>
        {(rs) => <p>{rs.data.map((r) => r.id).join(',')}</p>}
      </Resource>,
    )

    expect(await screen.findByText('1,2,3')).toBeInTheDocument()
    expect(fetch.mock.calls.map(([url]) => String(url))).toEqual([
      '/api/v2/users?sort=name',
      '/api/v2/users?sort=name&cursor=a',
      '/api/v2/users?sort=name&cursor=b',
    ])
  })

  test('a page that fails shows that Wishbone cannot be reached, and not half the rows', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof globalThis.fetch>(async (input) => (String(input).includes('cursor=') ? reply(503) : reply(200, rows(['1'], 'a')))),
    )
    render(
      <Resource<Rows> path="/api/v2/users" all>
        {(rs) => <p>rows: {rs.data.length}</p>}
      </Resource>,
    )

    expect(await screen.findByRole('heading', { name: "Can't reach Wishbone" })).toBeInTheDocument()
    expect(screen.queryByText(/rows:/)).not.toBeInTheDocument()
  })

  test('a session that ends between pages sends the app to the signed-out page', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof globalThis.fetch>(async (input) => (String(input).includes('cursor=') ? reply(401) : reply(200, rows(['1'], 'a')))),
    )
    const onSignedOut = vi.fn<() => void>()
    render(
      <SignedOutContext value={onSignedOut}>
        <Resource<Rows> path="/api/v2/users" all>
          {(rs) => <p>{rs.data.length}</p>}
        </Resource>
      </SignedOutContext>,
    )

    await waitFor(() => expect(onSignedOut).toHaveBeenCalledTimes(1))
  })

  test('without all, a collection is one page', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async () => reply(200, rows(['1'], 'a')))
    vi.stubGlobal('fetch', fetch)
    render(<Resource<Rows> path="/api/v2/users">{(rs) => <p>rows: {rs.data.length}</p>}</Resource>)

    expect(await screen.findByText('rows: 1')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})
