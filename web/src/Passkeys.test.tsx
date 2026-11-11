import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Passkey, User } from './api'
import { ada, grace, heading, ME, open, page, problem, sentBody, serve } from './test/fake'

const laptop: Passkey = {
  id: '3',
  name: 'Laptop',
  created_at: '2026-10-01T12:00:00Z',
  last_used_at: null,
  can_edit: true,
  can_delete: true,
  version: '"e1"',
}
const phone: Passkey = { ...laptop, id: '4', name: 'Phone', created_at: '2026-10-02T12:00:00Z', last_used_at: '2026-11-01T12:00:00Z' }

const creationOptions = { publicKey: { challenge: 'abc', rp: { id: 'x', name: 'Wishbone' } } }

/** A browser that answers navigator.credentials.create with a fixed credential. */
function stubBrowser(create: () => Promise<unknown>) {
  const parse = vi.fn<(o: unknown) => unknown>((o) => ({ parsed: o }))
  vi.stubGlobal('PublicKeyCredential', { parseCreationOptionsFromJSON: parse })
  const spy = vi.fn<() => Promise<unknown>>(create)
  Object.defineProperty(navigator, 'credentials', { value: { create: spy }, configurable: true })
  return { parse, create: spy }
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  Reflect.deleteProperty(navigator, 'credentials')
  window.history.replaceState(null, '', '/')
})

/** The signed-in user's own sign-in screen, with their sessions and passkeys. */
const own = (who: User, passkeys: Passkey[]) => ({
  [ME]: { status: 200, body: who },
  [`GET /api/v2/users/${who.id}`]: { status: 200, body: who },
  [`GET /api/v2/users/${who.id}/sessions`]: page([]),
  [`GET /api/v2/users/${who.id}/passkeys`]: page(passkeys),
})

const passkeysPart = async () => (await screen.findByRole('heading', { level: 2, name: 'Passkeys' })).closest('section')!

describe('the passkeys on the sign-in screen of the signed-in user', () => {
  test('lists each passkey with its name, when it was added, and when it was last used', async () => {
    serve(own(ada, [laptop, phone]))
    open('/users/7/sign-in')

    const rows = within(await passkeysPart()).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]!).getByText('Laptop')).toBeInTheDocument()
    expect(within(rows[0]!).getByText(/^Added /)).toBeInTheDocument()
    expect(within(rows[0]!).getByText('Never used')).toBeInTheDocument()
    expect(within(rows[1]!).getByText(/^Last used/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add a passkey' })).toBeInTheDocument()
  })

  test('says so when there are none', async () => {
    serve(own(ada, []))
    open('/users/7/sign-in')

    expect(await screen.findByText('No passkeys yet.')).toBeInTheDocument()
  })

  test('Add a passkey asks the browser for a credential and lists the new passkey', async () => {
    const user = userEvent.setup()
    const browser = stubBrowser(async () => ({ toJSON: () => ({ id: 'cred' }) }))
    const fetch = serve({
      ...own(ada, []),
      'POST /api/v2/users/7/passkeys/registration': { status: 200, body: creationOptions },
      'POST /api/v2/users/7/passkeys': { status: 201, body: laptop },
    })
    open('/users/7/sign-in')

    await user.click(await screen.findByRole('button', { name: 'Add a passkey' }))

    expect(await screen.findByText('Laptop')).toBeInTheDocument()
    expect(browser.parse).toHaveBeenCalledWith(creationOptions.publicKey)
    expect(browser.create).toHaveBeenCalledWith({ publicKey: { parsed: creationOptions.publicKey } })
    expect(sentBody(fetch, 'POST /api/v2/users/7/passkeys')).toEqual({ name: expect.any(String), credential: { id: 'cred' } })
    expect(screen.queryByText('No passkeys yet.')).not.toBeInTheDocument()
  })

  test('a cancelled prompt adds nothing and says so', async () => {
    const user = userEvent.setup()
    stubBrowser(async () => {
      throw new DOMException('cancelled', 'NotAllowedError')
    })
    const fetch = serve({ ...own(ada, []), 'POST /api/v2/users/7/passkeys/registration': { status: 200, body: creationOptions } })
    open('/users/7/sign-in')

    await user.click(await screen.findByRole('button', { name: 'Add a passkey' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/cancelled/i)
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/7/passkeys', expect.objectContaining({ method: 'POST' }))
  })

  test('a passkey the server already has shows the server’s words', async () => {
    const user = userEvent.setup()
    stubBrowser(async () => ({ toJSON: () => ({ id: 'cred' }) }))
    serve({
      ...own(ada, []),
      'POST /api/v2/users/7/passkeys/registration': { status: 200, body: creationOptions },
      'POST /api/v2/users/7/passkeys': problem(409, 'passkey_already_added', 'That passkey is already added.'),
    })
    open('/users/7/sign-in')

    await user.click(await screen.findByRole('button', { name: 'Add a passkey' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('That passkey is already added.')
  })

  test('a browser without passkey support says so', async () => {
    const user = userEvent.setup()
    serve(own(ada, []))
    open('/users/7/sign-in')

    await user.click(await screen.findByRole('button', { name: 'Add a passkey' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/can't add passkeys/i)
  })

  test('Rename changes the name with a merge patch', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...own(ada, [laptop, phone]),
      'PATCH /api/v2/users/7/passkeys/3': { status: 200, body: { ...laptop, name: 'Kitchen laptop', version: '"e2"' } },
    })
    open('/users/7/sign-in')
    await passkeysPart()

    await user.click(screen.getByRole('button', { name: 'Rename Laptop' }))
    const name = screen.getByLabelText('Name')
    await user.clear(name)
    await user.type(name, 'Kitchen laptop')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Kitchen laptop')).toBeInTheDocument()
    expect(screen.queryByLabelText('Name')).not.toBeInTheDocument()
    expect(sentBody(fetch, 'PATCH /api/v2/users/7/passkeys/3')).toEqual({ name: 'Kitchen laptop' })
    const sent = fetch.mock.calls.find(([, init]) => init?.method === 'PATCH')![1]!
    expect(sent.headers).toMatchObject({ 'Content-Type': 'application/merge-patch+json' })
  })

  test('a rename that fails says so and keeps the form', async () => {
    const user = userEvent.setup()
    serve({ ...own(ada, [laptop, phone]), 'PATCH /api/v2/users/7/passkeys/3': { status: 500 } })
    open('/users/7/sign-in')
    await passkeysPart()

    await user.click(screen.getByRole('button', { name: 'Rename Laptop' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not rename that passkey. Try again.')
    expect(screen.getByLabelText('Name')).toBeInTheDocument()
  })

  test('Delete removes a passkey at once when it is not the last', async () => {
    const user = userEvent.setup()
    const fetch = serve({ ...own(ada, [laptop, phone]), 'DELETE /api/v2/users/7/passkeys/3': { status: 204 } })
    open('/users/7/sign-in')
    await passkeysPart()

    await user.click(screen.getByRole('button', { name: 'Delete Laptop' }))

    await waitFor(() => expect(screen.queryByText('Laptop')).not.toBeInTheDocument())
    expect(screen.getByText('Phone')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/users/7/passkeys/3', expect.objectContaining({ method: 'DELETE' }))
  })

  test('a delete that fails says so and keeps the passkey', async () => {
    const user = userEvent.setup()
    serve({ ...own(ada, [laptop, phone]), 'DELETE /api/v2/users/7/passkeys/3': { status: 500 } })
    open('/users/7/sign-in')
    await passkeysPart()

    await user.click(screen.getByRole('button', { name: 'Delete Laptop' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not delete that passkey. Try again.')
    expect(screen.getByText('Laptop')).toBeInTheDocument()
  })

  test('a passkey that is already gone is done', async () => {
    const user = userEvent.setup()
    serve({ ...own(ada, [laptop, phone]), 'DELETE /api/v2/users/7/passkeys/3': problem(404, 'not_found') })
    open('/users/7/sign-in')
    await passkeysPart()

    await user.click(screen.getByRole('button', { name: 'Delete Laptop' }))

    await waitFor(() => expect(screen.queryByText('Laptop')).not.toBeInTheDocument())
  })
})

describe('deleting the last passkey', () => {
  const deleteLink = (name: string) => screen.findByRole('link', { name: `Delete ${name}` })

  test('opens a confirmation, and Keep it leaves the passkey', async () => {
    const user = userEvent.setup()
    const fetch = serve(own(ada, [laptop]))
    open('/users/7/sign-in')

    const link = await deleteLink('Laptop')
    expect(link).toHaveAttribute('href', '/users/7/passkeys/3/delete')
    await user.click(link)

    expect(await heading('Delete your last passkey?')).toBeInTheDocument()
    expect(screen.getByText("You'll need a new link or QR code to get back in.")).toBeInTheDocument()
    await user.click(screen.getByRole('link', { name: 'Keep it' }))

    expect(await heading('Your devices')).toBeInTheDocument()
    expect(screen.getByText('Laptop')).toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ method: 'DELETE' }))
  })

  test('the admin is told that only the person who runs Wishbone can make them a new link', async () => {
    serve(own(ada, [laptop]))
    open('/users/7/passkeys/3/delete')

    expect(await heading('Delete your last passkey?')).toBeInTheDocument()
    expect(screen.getByText("You'll need a new link or QR code to get back in.")).toBeInTheDocument()
    expect(screen.getByText('Only the person who runs Wishbone can make you a new link.')).toBeInTheDocument()
  })

  test('a user with no parents is told that only the admin can make them a new link', async () => {
    serve(own({ ...grace, parents: [] }, [laptop]))
    open('/users/8/passkeys/3/delete')

    expect(await heading('Delete your last passkey?')).toBeInTheDocument()
    expect(screen.getByText('Only the admin can make you a new link.')).toBeInTheDocument()
  })

  test('a user with a parent is told nothing more', async () => {
    const withParent = { ...grace, parents: [{ id: '7', name: 'Ada', avatar: null }] }
    serve(own(withParent, [laptop]))
    open('/users/8/passkeys/3/delete')

    expect(await heading('Delete your last passkey?')).toBeInTheDocument()
    expect(screen.queryByText(/can make you a new link/)).not.toBeInTheDocument()
  })

  test('for someone else, it names the user', async () => {
    serve({
      [ME]: { status: 200, body: ada },
      'GET /api/v2/users/8': { status: 200, body: grace },
      'GET /api/v2/users/8/passkeys': page([{ ...laptop, can_edit: false }]),
    })
    open('/users/8/passkeys/3/delete')

    expect(await heading("Delete Grace's last passkey?")).toBeInTheDocument()
    expect(screen.getByText('Grace will need a new link or QR code to sign in on a new device.')).toBeInTheDocument()
  })

  test('the action deletes the passkey and returns to the sign-in screen', async () => {
    const user = userEvent.setup()
    let gone = false
    serve({
      ...own(ada, [laptop]),
      'GET /api/v2/users/7/passkeys': () => page(gone ? [] : [laptop]),
      'DELETE /api/v2/users/7/passkeys/3': () => {
        gone = true
        return { status: 204 }
      },
    })
    open('/users/7/passkeys/3/delete')

    await user.click(await screen.findByRole('button', { name: 'Delete this passkey' }))

    expect(await heading('Your devices')).toBeInTheDocument()
    expect(await screen.findByText('No passkeys yet.')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/users/7/sign-in')
  })

  test('a delete that fails says so and stays', async () => {
    const user = userEvent.setup()
    serve({ ...own(ada, [laptop]), 'DELETE /api/v2/users/7/passkeys/3': { status: 500 } })
    open('/users/7/passkeys/3/delete')

    await user.click(await screen.findByRole('button', { name: 'Delete this passkey' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
    expect(window.location.pathname).toBe('/users/7/passkeys/3/delete')
  })

  test('a passkey that is not there shows the missing-page message', async () => {
    serve(own(ada, [laptop]))
    open('/users/7/passkeys/99/delete')

    expect(await heading("We can't find that page")).toBeInTheDocument()
  })
})

describe('the passkeys on the sign-in screen of another user', () => {
  const routes = (passkeys: Passkey[]) => ({
    [ME]: { status: 200, body: ada },
    'GET /api/v2/users/8': { status: 200, body: grace },
    'GET /api/v2/users/8/sessions': page([]),
    'GET /api/v2/users/8/passkeys': page(passkeys),
  })

  test('shows Delete from can_delete, and neither Rename nor Add a passkey', async () => {
    serve(routes([{ ...laptop, can_edit: false }, { ...phone, can_edit: false }]))
    open('/users/8/sign-in')

    await passkeysPart()
    expect(screen.getByRole('button', { name: 'Delete Laptop' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Rename/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add a passkey' })).not.toBeInTheDocument()
  })

  test('shows no Delete when can_delete is false', async () => {
    serve(routes([{ ...laptop, can_edit: false, can_delete: false }, { ...phone, can_edit: false, can_delete: false }]))
    open('/users/8/sign-in')

    await passkeysPart()
    expect(screen.queryByRole('button', { name: /^Delete/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /^Delete/ })).not.toBeInTheDocument()
  })

  test('has no passkeys part when the caller cannot manage sessions', async () => {
    const fetch = serve({
      [ME]: { status: 200, body: ada },
      'GET /api/v2/users/8': { status: 200, body: { ...grace, can_manage_sessions: false } },
    })
    open('/users/8/sign-in')

    await heading('Sign Grace in')
    expect(screen.queryByRole('heading', { level: 2, name: 'Passkeys' })).not.toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/8/passkeys', expect.anything())
  })
})
