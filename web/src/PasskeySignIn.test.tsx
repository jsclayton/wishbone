import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ada, heading, ME, open, page, problem, serve } from './test/fake'

const requestOptions = { publicKey: { challenge: 'abc', rpId: 'x' } }
const credential = { id: 'cred', toJSON: () => ({ id: 'cred', type: 'public-key' }) }

function stubBrowser(get: () => Promise<unknown>) {
  const parse = vi.fn<(o: unknown) => unknown>((o) => ({ parsed: o }))
  vi.stubGlobal('PublicKeyCredential', { parseRequestOptionsFromJSON: parse })
  const spy = vi.fn<(o: unknown) => Promise<unknown>>(get)
  Object.defineProperty(navigator, 'credentials', { value: { get: spy }, configurable: true })
  return { parse, get: spy }
}

const OPTIONS = 'POST /api/v2/login/passkey/options'
const LOGIN = 'POST /api/v2/login/passkey'
const button = () => screen.findByRole('button', { name: 'Sign in with passkey' })

let signedIn = false
const routes = () => ({
  [ME]: () => (signedIn ? { status: 200, body: ada } : problem(401, 'unauthorized')),
  'GET /api/v2/setup': { status: 200, body: { is_needed: false } },
  'GET /api/v2/users/me/sessions': page([]),
})

beforeEach(() => {
  signedIn = false
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  Reflect.deleteProperty(navigator, 'credentials')
  window.history.replaceState(null, '', '/')
})

test('the signed-out page offers Sign in with passkey next to the login link words', async () => {
  serve(routes())
  open('/')

  expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
  expect(await button()).toBeEnabled()
  expect(screen.getByText(/Open your login link to sign in/)).toBeInTheDocument()
})

test('signing in with a passkey asks for a credential with no username and lands signed in', async () => {
  const user = userEvent.setup()
  const browser = stubBrowser(async () => credential)
  const fetch = serve({
    ...routes(),
    [OPTIONS]: { status: 200, body: requestOptions },
    [LOGIN]: () => {
      signedIn = true
      return { status: 204 }
    },
  })
  open('/')

  await user.click(await button())

  expect(await heading('Home')).toBeInTheDocument()
  expect(browser.parse).toHaveBeenCalledWith(requestOptions.publicKey)
  const send = fetch.mock.calls.find(([url]) => url === '/api/v2/login/passkey')?.[1]
  expect(JSON.parse(String(send?.body))).toEqual({ credential: { id: 'cred', type: 'public-key' } })
})

test('a rejected passkey says it did not work, and Try again goes back', async () => {
  const user = userEvent.setup()
  stubBrowser(async () => credential)
  serve({
    ...routes(),
    [OPTIONS]: { status: 200, body: requestOptions },
    [LOGIN]: problem(401, 'passkey_login_failed', "That passkey didn't work."),
  })
  open('/')

  await user.click(await button())

  expect(await heading("That didn't work")).toBeInTheDocument()
  expect(document.querySelector('main')).toHaveFocus()
  await user.click(screen.getByRole('button', { name: 'Try again' }))
  expect(await button()).toBeEnabled()
  expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
})

test('cancelling the browser prompt says it did not work', async () => {
  const user = userEvent.setup()
  stubBrowser(async () => {
    throw new DOMException('cancelled', 'NotAllowedError')
  })
  serve({ ...routes(), [OPTIONS]: { status: 200, body: requestOptions } })
  open('/')

  await user.click(await button())

  expect(await heading("That didn't work")).toBeInTheDocument()
})

test('a browser without passkey support says it did not work', async () => {
  const user = userEvent.setup()
  serve(routes())
  open('/')

  await user.click(await button())

  expect(await heading("That didn't work")).toBeInTheDocument()
})

test('too many attempts says to wait', async () => {
  const user = userEvent.setup()
  stubBrowser(async () => credential)
  serve({ ...routes(), [OPTIONS]: problem(429, 'rate_limited') })
  open('/')

  await user.click(await button())

  expect(await heading('Too many tries')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Try again' }))
  expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
})

test('a sign-in the browser keeps no cookie for says it did not work', async () => {
  const user = userEvent.setup()
  stubBrowser(async () => credential)
  serve({ ...routes(), [OPTIONS]: { status: 200, body: requestOptions }, [LOGIN]: { status: 204 } })
  open('/')

  await user.click(await button())

  // The server said yes, but the next look at who is signed in still says no.
  expect(await heading("That didn't work")).toBeInTheDocument()
})

test('the failure page is a history entry that Back leaves', async () => {
  const user = userEvent.setup()
  serve(routes())
  open('/')
  await user.click(await button())
  await heading("That didn't work")

  window.history.back()

  expect(await heading('Welcome to Wishbone')).toBeInTheDocument()
})
