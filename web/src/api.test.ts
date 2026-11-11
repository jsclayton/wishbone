import { getMe, getSetup, logOut, postSetup, type User } from './api'

const ada: User = {
  id: '7',
  name: 'Ada',
  last_name: null,
  avatar: null,
  birthday: { month: 12, day: 10, year: null },
  is_admin: true,
  good_to_know: null,
  timezone: null,
  profile_confirmed_at: '2026-11-08T12:00:00Z',
  created_at: '2026-11-08T12:00:00Z',
  can_edit: true,
  can_make_login_link: true,
  can_manage_sessions: true,
  can_confirm_profile: false,
  can_edit_parents: false,
  can_deactivate: false,
  can_reactivate: false,
  can_add_occasion: true,
  parents: [],
  children: [],
}

function reply(status: number, body?: unknown) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': status >= 400 ? 'application/problem+json' : 'application/json' },
  })
}

afterEach(() => vi.unstubAllGlobals())

/** The arguments of the first call to fetch. */
function firstCall(fetch: ReturnType<typeof stubFetch>) {
  const call = fetch.mock.calls[0]
  if (!call) throw new Error('fetch was not called')
  return call
}

function stubFetch(response: Response) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response)
  vi.stubGlobal('fetch', fetch)
  return fetch
}

describe('getMe', () => {
  test('returns the signed-in user from /users/me', async () => {
    const fetch = stubFetch(reply(200, ada))
    expect(await getMe()).toEqual(ada)
    expect(firstCall(fetch)[0]).toBe('/api/v2/users/me')
  })

  test('returns null for a browser with no session', async () => {
    stubFetch(reply(401, { status: 401, code: 'unauthorized', detail: 'Sign in to do this.' }))
    expect(await getMe()).toBeNull()
  })

  test('throws when the server fails, so the app says it cannot reach Wishbone', async () => {
    stubFetch(reply(503, { status: 503, code: 'service_unavailable', detail: 'down' }))
    await expect(getMe()).rejects.toThrow('503')
  })
})

describe('getSetup', () => {
  test('says whether setup is needed', async () => {
    const fetch = stubFetch(reply(200, { is_needed: true }))
    expect(await getSetup()).toBe(true)
    expect(firstCall(fetch)[0]).toBe('/api/v2/setup')
  })
})

describe('postSetup', () => {
  const input = { setup_code: 'ABCD-EFGH-JKMN', name: 'Ada', birthday: { month: 12, day: 10, year: null } }

  test('sends the setup as JSON and returns the new user', async () => {
    const fetch = stubFetch(reply(201, ada))
    expect(await postSetup(input)).toEqual({ ok: true, user: ada })
    const [url, init] = firstCall(fetch)
    expect(url).toBe('/api/v2/setup')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(init?.body as string)).toEqual(input)
  })

  test.each([
    [422, 'setup_code_invalid', 'code'],
    [409, 'setup_complete', 'complete'],
    [429, 'rate_limited', 'rate_limited'],
    [422, 'invalid_input', 'invalid'],
  ])('a %i with code %s is the problem %s, with the server detail', async (status, code, problem) => {
    stubFetch(reply(status, { status, code, detail: 'Words from the server.' }))
    expect(await postSetup(input)).toEqual({ ok: false, problem, detail: 'Words from the server.' })
  })

  test('any other failure throws', async () => {
    stubFetch(reply(500, { status: 500, code: 'internal_error', detail: 'oops' }))
    await expect(postSetup(input)).rejects.toThrow('500')
  })
})

describe('logOut', () => {
  test('revokes the current session', async () => {
    const fetch = stubFetch(reply(204))
    await logOut()
    const [url, init] = firstCall(fetch)
    expect(url).toBe('/api/v2/users/me/sessions/current')
    expect(init?.method).toBe('DELETE')
  })

  test('throws when the server does not sign the user out', async () => {
    stubFetch(reply(500, { status: 500, code: 'internal_error', detail: 'oops' }))
    await expect(logOut()).rejects.toThrow('500')
  })

  test('a 401 is already signed out', async () => {
    stubFetch(reply(401, { status: 401, code: 'unauthorized', detail: 'no' }))
    await expect(logOut()).resolves.toBeUndefined()
  })
})
