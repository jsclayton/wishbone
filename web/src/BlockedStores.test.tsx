import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { BlockedStore, User } from './api'
import { ada, grace, heading, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const asAda = { [ME]: { status: 200, body: ada } }
const graceSelf: User = { ...grace, profile_confirmed_at: '2026-11-08T13:00:00Z', can_edit: true }
const asGrace = { [ME]: { status: 200, body: graceSelf } }

const store = (id: string, name: string): BlockedStore => ({ id, store: name, created_at: '2026-11-09T12:00:00Z', can_delete: true })
const temu = store('3', 'temu.com')
const shein = store('4', 'shein.com')
const LIST = 'GET /api/v2/blocked-stores'

describe('Menu', () => {
  test('has Blocked stores in the admin part', async () => {
    serve(asAda)
    open('/menu')

    const part = (await screen.findByRole('heading', { name: 'For the admin' })).parentElement!
    expect(within(part).getByRole('link', { name: 'Blocked stores' })).toHaveAttribute('href', '/blocked-stores')
  })

  test('has no Blocked stores for anyone else', async () => {
    serve(asGrace)
    open('/menu')

    await screen.findByRole('link', { name: 'Groups' })
    expect(screen.queryByRole('link', { name: 'Blocked stores' })).not.toBeInTheDocument()
  })
})

describe('the Blocked stores screen', () => {
  test('lists every blocked store, following the cursor, with a way to allow each', async () => {
    const fetch = serve({
      ...asAda,
      [LIST]: page([shein], 'abc'),
      [`${LIST}?cursor=abc`]: page([temu]),
    })
    open('/blocked-stores')

    expect(await heading('Blocked stores')).toBeInTheDocument()
    const list = await screen.findByRole('list', { name: 'Blocked stores' })
    expect(within(list).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['shein.comAllow', 'temu.comAllow'])
    expect(screen.getByRole('button', { name: 'Allow temu.com' })).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/blocked-stores?cursor=abc', expect.anything())
  })

  test('says so when no store is blocked, and does not list the items on lists', async () => {
    serve({ ...asAda, [LIST]: page([]) })
    open('/blocked-stores')

    expect(await screen.findByText('No stores are blocked.')).toBeInTheDocument()
    expect(screen.queryByText(/Already on lists/)).not.toBeInTheDocument()
  })

  test('shows the missing-page message to anyone but the admin, and asks for nothing', async () => {
    const fetch = serve(asGrace)
    open('/blocked-stores')

    expect(await heading("We can't find that page")).toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/blocked-stores', expect.anything())
  })

  test('blocks the store that the admin types, and lists it', async () => {
    const user = userEvent.setup()
    let blocked = false
    const fetch = serve({
      ...asAda,
      [LIST]: () => page(blocked ? [temu] : []),
      'POST /api/v2/blocked-stores': () => {
        blocked = true
        return { status: 201, body: temu }
      },
    })
    open('/blocked-stores')
    await screen.findByText('No stores are blocked.')

    await user.type(screen.getByLabelText('Block a store'), ' https://www.temu.com/p/1 ')
    await user.click(screen.getByRole('button', { name: 'Block' }))

    expect(await screen.findByRole('button', { name: 'Allow temu.com' })).toBeInTheDocument()
    expect(sentBody(fetch, 'POST /api/v2/blocked-stores')).toEqual({ store: 'https://www.temu.com/p/1' })
    expect(screen.getByLabelText('Block a store')).toHaveValue('')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  test('asks for a store before it sends anything', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...asAda, [LIST]: page([]) })
    open('/blocked-stores')
    await screen.findByText('No stores are blocked.')

    await user.click(screen.getByRole('button', { name: 'Block' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Enter a store, such as temu.com.')
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/blocked-stores', expect.objectContaining({ method: 'POST' }))
  })

  test("shows the server's words when the text is not a store, and keeps what was typed", async () => {
    const user = userEvent.setup()
    serve({
      ...asAda,
      [LIST]: page([]),
      'POST /api/v2/blocked-stores': problem(422, 'invalid_input', "Enter a store's web address, such as temu.com"),
    })
    open('/blocked-stores')
    await screen.findByText('No stores are blocked.')

    await user.type(screen.getByLabelText('Block a store'), 'nonsense')
    await user.click(screen.getByRole('button', { name: 'Block' }))

    expect(await screen.findByRole('alert')).toHaveTextContent("Enter a store's web address, such as temu.com")
    expect(screen.getByLabelText('Block a store')).toHaveValue('nonsense')
  })

  test('treats a store that is already blocked as done', async () => {
    const user = userEvent.setup()
    serve({
      ...asAda,
      [LIST]: page([temu]),
      'POST /api/v2/blocked-stores': problem(409, 'already_blocked', 'That store is already blocked.'),
    })
    open('/blocked-stores')
    await screen.findByRole('button', { name: 'Allow temu.com' })

    await user.type(screen.getByLabelText('Block a store'), 'temu.com')
    await user.click(screen.getByRole('button', { name: 'Block' }))

    await vi.waitFor(() => expect(screen.getByLabelText('Block a store')).toHaveValue(''))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  test('says so when blocking fails for another reason', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, [LIST]: page([]), 'POST /api/v2/blocked-stores': problem(500, 'internal_error') })
    open('/blocked-stores')
    await screen.findByText('No stores are blocked.')

    await user.type(screen.getByLabelText('Block a store'), 'temu.com')
    await user.click(screen.getByRole('button', { name: 'Block' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
  })

  test('allows a store again, with no confirmation screen', async () => {
    const user = userEvent.setup()
    let allowed = false
    const fetch = serve({
      ...asAda,
      [LIST]: () => page(allowed ? [shein] : [shein, temu]),
      'DELETE /api/v2/blocked-stores/3': () => {
        allowed = true
        return { status: 204 }
      },
    })
    open('/blocked-stores')

    await user.click(await screen.findByRole('button', { name: 'Allow temu.com' }))

    await vi.waitFor(() => expect(screen.queryByRole('button', { name: 'Allow temu.com' })).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Allow shein.com' })).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/blocked-stores/3', expect.objectContaining({ method: 'DELETE' }))
    expect(window.location.pathname).toBe('/blocked-stores')
  })

  test('says so when allowing fails, and keeps the store listed', async () => {
    const user = userEvent.setup()
    serve({ ...asAda, [LIST]: page([temu]), 'DELETE /api/v2/blocked-stores/3': problem(500, 'internal_error') })
    open('/blocked-stores')

    await user.click(await screen.findByRole('button', { name: 'Allow temu.com' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(screen.getByRole('button', { name: 'Allow temu.com' })).toBeInTheDocument()
  })
})
