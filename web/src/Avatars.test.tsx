import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ImageSummary, User } from './api'
import { ada, grace, heading, ME, open, page, problem, sentBody, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const photo: ImageSummary = { id: '31', url: '/api/v2/images/31', width: 40, height: 30 }
const withPhoto = (user: User): User => ({ ...user, avatar: photo })
const graceSelf: User = { ...grace, profile_confirmed_at: '2026-11-08T13:00:00Z' }
const file = (type = 'image/png') => new File([new Uint8Array([1, 2, 3])], 'me.png', { type })
const bytesOf = (body: unknown) => Array.from(new Uint8Array(body as ArrayBuffer))
const uploaded = { status: 201, body: { ...photo, content_type: 'image/jpeg' } }

/** The initials of the avatars on the screen, in order. */
const initialsShown = () => Array.from(document.querySelectorAll('[data-initials]')).map((el) => el.getAttribute('data-initials'))
/** The addresses of the photos on the screen, in order. */
const photosShown = () => Array.from(document.querySelectorAll('main img')).map((el) => el.getAttribute('src'))

describe('users show their avatar, or their initials', () => {
  test('the Users screen shows each user next to their name', async () => {
    serve({
      [ME]: { status: 200, body: ada },
      'GET /api/v2/users': page([ada, withPhoto(grace), { ...grace, id: '9', name: 'Zed', last_name: null }]),
    })
    open('/users')

    await heading('Users')

    await waitFor(() => expect(initialsShown()).toEqual(['AL', 'Z']))
    expect(photosShown()).toEqual(['/api/v2/images/31'])
    const row = screen.getByRole('link', { name: 'Grace Hopper' })
    expect(row.querySelector('img')).not.toBeNull()
  })

  test('Menu shows the signed-in user', async () => {
    serve({ [ME]: { status: 200, body: withPhoto(ada) } })
    open('/menu')

    await heading('Menu')

    expect(photosShown()).toEqual(['/api/v2/images/31'])
    expect(initialsShown()).toEqual([])
  })

  test("a user's page shows the user", async () => {
    serve({ [ME]: { status: 200, body: ada }, 'GET /api/v2/users/8': { status: 200, body: grace } })
    open('/users/8')

    await heading('Grace Hopper')

    expect(initialsShown()).toEqual(['GH'])
  })

  test('the avatar is not read out: the name sits next to it', async () => {
    serve({ [ME]: { status: 200, body: withPhoto(ada) } })
    open('/menu')
    await heading('Menu')

    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })
})

describe('the photo on the profile screen', () => {
  const routes = (body: User = graceSelf) => ({
    [ME]: { status: 200, body: ada },
    'GET /api/v2/users/8': { status: 200, body },
    'GET /api/v2/occasions?user_id=8': page([]),
  })

  test('shows the initials, and Add a photo', async () => {
    serve(routes())
    open('/users/8/profile')
    await heading("Grace's profile")

    expect(initialsShown()).toEqual(['GH'])
    expect(screen.getByRole('button', { name: 'Add a photo' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Remove photo' })).not.toBeInTheDocument()
  })

  test('a chosen file is uploaded as it is, then attached by its ID', async () => {
    const user = userEvent.setup()
    const chosen = file('image/png')
    const fetch = serve({
      ...routes(),
      'POST /api/v2/images': uploaded,
      'PATCH /api/v2/users/8': { status: 200, body: withPhoto(graceSelf) },
    })
    open('/users/8/profile')
    await heading("Grace's profile")

    await user.upload(screen.getByLabelText('Add a photo'), chosen)

    await waitFor(() => expect(photosShown()).toEqual(['/api/v2/images/31']))
    const post = fetch.mock.calls.find(([, init]) => init?.method === 'POST')!
    expect(post[0]).toBe('/api/v2/images')
    expect(bytesOf(post[1]?.body)).toEqual([1, 2, 3])
    expect(new Headers(post[1]?.headers).get('Content-Type')).toBe('image/png')
    expect(sentBody(fetch, 'PATCH /api/v2/users/8')).toEqual({ avatar_image_id: '31' })
    const patch = fetch.mock.calls.find(([, init]) => init?.method === 'PATCH')!
    expect(new Headers(patch[1]?.headers).get('Content-Type')).toBe('application/merge-patch+json')
    expect(initialsShown()).toEqual([])
    expect(screen.getByRole('button', { name: 'Change photo' })).toBeInTheDocument()
  })

  test('the photo is saved on its own: Save sends nothing about it', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...routes(),
      'POST /api/v2/images': uploaded,
      'PATCH /api/v2/users/8': (init) => ({ status: 200, body: { ...withPhoto(graceSelf), ...JSON.parse(String(init?.body)), avatar: photo } }),
    })
    open('/users/8/profile')
    await heading("Grace's profile")
    await user.upload(screen.getByLabelText('Add a photo'), file())
    await screen.findByRole('button', { name: 'Change photo' })

    await user.type(screen.getByLabelText('Good to know'), 'Tea')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await screen.findByText('Saved.')
    const patches = fetch.mock.calls.filter(([, init]) => init?.method === 'PATCH')
    expect(patches.map(([, init]) => JSON.parse(String(init?.body)))).toEqual([{ avatar_image_id: '31' }, { good_to_know: 'Tea' }])
  })

  test('Remove photo sends null and brings the initials back', async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...routes(withPhoto(graceSelf)),
      'PATCH /api/v2/users/8': { status: 200, body: graceSelf },
    })
    open('/users/8/profile')
    await heading("Grace's profile")
    expect(photosShown()).toEqual(['/api/v2/images/31'])

    await user.click(screen.getByRole('button', { name: 'Remove photo' }))

    await waitFor(() => expect(initialsShown()).toEqual(['GH']))
    expect(sentBody(fetch, 'PATCH /api/v2/users/8')).toEqual({ avatar_image_id: null })
    expect(screen.queryByRole('button', { name: 'Remove photo' })).not.toBeInTheDocument()
  })

  test("shows the server's words when it refuses the file, and keeps the photo", async () => {
    const user = userEvent.setup()
    const fetch = serve({
      ...routes(),
      'POST /api/v2/images': problem(422, 'image_invalid', 'Use a JPEG, PNG, GIF, or WebP image.'),
    })
    open('/users/8/profile')
    await heading("Grace's profile")

    await user.upload(screen.getByLabelText('Add a photo'), file('image/png'))

    expect(await screen.findByRole('alert')).toHaveTextContent('Use a JPEG, PNG, GIF, or WebP image.')
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/8', expect.objectContaining({ method: 'PATCH' }))
    expect(initialsShown()).toEqual(['GH'])
  })

  test('says so when the file is too large', async () => {
    const user = userEvent.setup()
    serve({
      ...routes(),
      'POST /api/v2/images': problem(413, 'image_too_large', 'Use an image smaller than 10 MB.'),
    })
    open('/users/8/profile')
    await heading("Grace's profile")

    await user.upload(screen.getByLabelText('Add a photo'), file())

    expect(await screen.findByRole('alert')).toHaveTextContent('Use an image smaller than 10 MB.')
  })

  test('the own profile updates the avatar in Menu', async () => {
    const user = userEvent.setup()
    serve({
      [ME]: { status: 200, body: ada },
      'GET /api/v2/users/7': { status: 200, body: ada },
      'GET /api/v2/occasions?user_id=7': page([]),
      'POST /api/v2/images': uploaded,
      'PATCH /api/v2/users/7': { status: 200, body: withPhoto(ada) },
    })
    open('/users/7/profile')
    await heading('Your profile')

    await user.upload(screen.getByLabelText('Add a photo'), file())
    await screen.findByRole('button', { name: 'Change photo' })
    await user.click(screen.getByRole('link', { name: 'Menu' }))

    await heading('Menu')
    expect(photosShown()).toEqual(['/api/v2/images/31'])
  })
})

describe('the Add a photo step of the first sign-in', () => {
  const newbie: User = { ...grace, id: '9', name: 'Zed', last_name: null, can_confirm_profile: true, is_admin: false }
  const confirmedNewbie = { ...newbie, can_confirm_profile: false, profile_confirmed_at: '2026-11-09T09:00:00Z' }

  function signIn(extra: Parameters<typeof serve>[0] = {}) {
    let confirmed = false
    return serve({
      [ME]: () => ({ status: 200, body: confirmed ? confirmedNewbie : newbie }),
      'POST /api/v2/users/9/confirm-profile': () => {
        confirmed = true
        return { status: 200, body: confirmedNewbie }
      },
      ...extra,
    })
  }

  test('comes after the profile, and the profile does not confirm until the step is done', async () => {
    const user = userEvent.setup()
    const fetch = signIn()
    open('/')
    await heading('Finish your profile')

    await user.click(screen.getByRole('button', { name: 'Continue' }))

    expect(await heading('Add a photo')).toBeInTheDocument()
    expect(screen.getByText('Help people recognize you')).toBeInTheDocument()
    expect(initialsShown()).toEqual(['Z'])
    expect(screen.queryByRole('link', { name: 'Menu' })).not.toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/users/9/confirm-profile', expect.anything())
  })

  test('the initials follow the names the person entered', async () => {
    const user = userEvent.setup()
    signIn({ 'PATCH /api/v2/users/9': { status: 200, body: newbie } })
    open('/')
    await heading('Finish your profile')

    await user.type(screen.getByLabelText('Last name (optional)'), 'Zeta')
    await user.click(screen.getByRole('button', { name: 'Continue' }))

    await heading('Add a photo')
    expect(initialsShown()).toEqual(['ZZ'])
  })

  test('Not now skips it: the profile is confirmed and the app opens', async () => {
    const user = userEvent.setup()
    const fetch = signIn()
    open('/menu')
    await heading('Finish your profile')
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await heading('Add a photo')

    await user.click(screen.getByRole('button', { name: 'Not now' }))

    expect(await heading('Menu')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/users/9/confirm-profile', expect.objectContaining({ method: 'POST' }))
    expect(fetch).not.toHaveBeenCalledWith('/api/v2/images', expect.anything())
  })

  test('a chosen photo is uploaded and attached, and Use this photo goes on', async () => {
    const user = userEvent.setup()
    const chosen = file()
    const fetch = signIn({
      'POST /api/v2/images': uploaded,
      'PATCH /api/v2/users/9': { status: 200, body: withPhoto(newbie) },
    })
    open('/')
    await heading('Finish your profile')
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await heading('Add a photo')

    await user.upload(document.querySelector('input[type="file"]')!, chosen)

    expect(await screen.findByRole('button', { name: 'Use this photo' })).toBeInTheDocument()
    expect(photosShown()).toEqual(['/api/v2/images/31'])
    expect(sentBody(fetch, 'PATCH /api/v2/users/9')).toEqual({ avatar_image_id: '31' })
    expect(screen.getByRole('button', { name: 'Choose a different photo' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Not now' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Use this photo' }))

    expect(await heading('Home')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/users/9/confirm-profile', expect.objectContaining({ method: 'POST' }))
  })

  test('a file the server refuses shows its words, and Not now is still there', async () => {
    const user = userEvent.setup()
    signIn({ 'POST /api/v2/images': problem(422, 'image_invalid', 'Use a JPEG, PNG, GIF, or WebP image.') })
    open('/')
    await heading('Finish your profile')
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await heading('Add a photo')

    await user.upload(document.querySelector('input[type="file"]')!, file())

    expect(await screen.findByRole('alert')).toHaveTextContent('Use a JPEG, PNG, GIF, or WebP image.')
    expect(screen.getByRole('button', { name: 'Not now' })).toBeEnabled()
  })

  test('each step is its own history entry: Back returns to the profile as it was left, and Forward to the photo', async () => {
    const user = userEvent.setup()
    const fetch = signIn({ 'PATCH /api/v2/users/9': { status: 200, body: newbie } })
    open('/')
    await heading('Finish your profile')
    await user.type(screen.getByLabelText('Last name (optional)'), 'Zeta')
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await heading('Add a photo')

    window.history.back()

    await heading('Finish your profile')
    expect(screen.getByLabelText('Last name (optional)')).toHaveValue('Zeta')
    // Continue again sends nothing: the server already has it.
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await heading('Add a photo')
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PATCH')).toHaveLength(1)

    window.history.back()
    await heading('Finish your profile')
    window.history.forward()
    await heading('Add a photo')
  })

  test('finishing leaves no step in the history entry', async () => {
    const user = userEvent.setup()
    signIn()
    open('/')
    await heading('Finish your profile')
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await user.click(await screen.findByRole('button', { name: 'Not now' }))

    await heading('Home')
    expect((window.history.state as { firstStep?: string } | null)?.firstStep).toBeUndefined()
  })

  // The entry still marks an in-app entry before it, so Back works,
  // and it keeps the path, the query, and the hash.
  test('finishing keeps the address and marks the entry as an in-app entry', async () => {
    const user = userEvent.setup()
    signIn()
    open('/menu?tab=lists#top')
    await heading('Finish your profile')
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await user.click(await screen.findByRole('button', { name: 'Not now' }))

    await waitFor(() => expect(screen.queryByRole('heading', { level: 1, name: 'Add a photo' })).not.toBeInTheDocument())
    expect(window.history.state).toEqual({ inApp: true })
    expect(window.location.pathname + window.location.search + window.location.hash).toBe('/menu?tab=lists#top')
  })

  test('has no top row, so nobody wanders off before the profile is done', async () => {
    const user = userEvent.setup()
    signIn()
    open('/')
    await heading('Finish your profile')
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await heading('Add a photo')

    expect(screen.queryByRole('navigation', { name: 'Top' })).not.toBeInTheDocument()
    expect(within(document.body).queryByRole('link', { name: 'Wishbone' })).not.toBeInTheDocument()
  })
})
