import { screen, within } from '@testing-library/react'
import type { User } from './api'
import { ada, family, grace, heading, ME, open, page, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const signedInAsAda = { [ME]: { status: 200, body: ada } }
const paused: User = { ...grace, deactivated_at: '2026-11-08T14:00:00Z', can_deactivate: false, can_reactivate: true }
const hedy: User = { ...grace, id: '9', name: 'Hedy', last_name: 'Lamarr' }

describe('the groups screens and a deactivated user', () => {
  test('a deactivated member shows as deactivated and has no Remove, because the server refuses it', async () => {
    serve({
      ...signedInAsAda,
      'GET /api/v2/groups/5': { status: 200, body: family },
      'GET /api/v2/groups/5/users': page([paused, hedy]),
      'GET /api/v2/groups/5/groups': page([]),
    })
    open('/groups/5')

    const users = within(await screen.findByRole('region', { name: 'Users' }))
    expect((await users.findAllByRole('listitem')).map((item) => item.textContent)).toEqual(['Grace Hopper Deactivated', 'Hedy LamarrRemove'])
    expect(users.queryByRole('button', { name: 'Remove Grace Hopper' })).not.toBeInTheDocument()
    expect(users.getByRole('button', { name: 'Remove Hedy Lamarr' })).toBeInTheDocument()
  })

  test('Add a user does not offer a deactivated user, because the server refuses it', async () => {
    serve({
      ...signedInAsAda,
      'GET /api/v2/groups/5': { status: 200, body: family },
      'GET /api/v2/groups/5/users': page([]),
      'GET /api/v2/groups/5/groups': page([]),
      'GET /api/v2/users': page([ada, paused, hedy]),
    })
    open('/groups/5/users')
    await heading('Add a user to My family')

    const rows = await screen.findAllByRole('listitem')
    expect(rows.map((row) => row.textContent)).toEqual(['Ada Lovelace (you)Add', 'Hedy LamarrAdd'])
  })
})
