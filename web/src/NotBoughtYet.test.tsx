import { screen } from '@testing-library/react'
import type { Gift } from './api'
import { aGift, ada, heading, ME, open, page, problem, serve } from './test/fake'

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const GIVING = 'GET /api/v2/gifts?giver_id=me&state=open&needs_attention=true'
const home = { [ME]: { status: 200, body: ada }, 'GET /api/v2/groups?includes_me=true': page([]) }
const soon: Gift = { ...aGift, id: '70', title: 'Socks', quantity: 2, needs_attention: true }
const changed: Gift = { ...aGift, id: '72', title: 'Pan', needs_attention: true, is_bought: true, flags: [{ code: 'changed', fields: [{ field: 'title', old: 'Pan', new: 'Wok' }] }] }
const removed: Gift = { ...aGift, id: '73', title: 'Jar', needs_attention: true, is_bought: true, flags: [{ code: 'removed' }] }

describe('Home', () => {
  test('has a Giving button beside My lists', async () => {
    serve({ ...home, [GIVING]: page([]) })
    open('/')

    await heading('Home')
    expect(screen.getByRole('link', { name: 'Giving' })).toHaveAttribute('href', '/giving')
    expect(screen.getByRole('link', { name: 'My lists' })).toHaveAttribute('href', '/lists')
  })

  test('Not bought yet lists the gifts that need attention, each opening its row in Giving', async () => {
    serve({ ...home, [GIVING]: page([soon]) })
    open('/')

    expect(await screen.findByRole('heading', { level: 2, name: 'Not bought yet' })).toBeInTheDocument()
    const link = screen.getByRole('link', { name: /Socks \(2\) for Mom/ })
    expect(link).toHaveAttribute('href', '/giving#gift-70')
  })

  test('names what happened to a gift that was changed or removed, which "not bought" does not describe', async () => {
    serve({ ...home, [GIVING]: page([soon, changed, removed]) })
    open('/')

    const items = await screen.findAllByRole('listitem')
    expect(items.map((i) => i.textContent)).toEqual(['Socks (2) for Mom', 'Pan for MomChanged since you picked it', 'Jar for MomRemoved from the list'])
    expect(screen.getByRole('link', { name: /Pan for Mom/ })).toHaveAttribute('href', '/giving#gift-72')
  })

  test('shows no Not bought yet when no gift needs attention', async () => {
    serve({ ...home, [GIVING]: page([]) })
    open('/')

    await heading('Home')
    await screen.findByRole('link', { name: 'Giving' })
    expect(screen.queryByRole('heading', { name: 'Not bought yet' })).not.toBeInTheDocument()
  })

  test('leaves the rest of Home alone when the gifts cannot load', async () => {
    serve({ ...home, [GIVING]: problem(500, 'internal_error') })
    open('/')

    expect(await heading('Home')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Not bought yet' })).not.toBeInTheDocument()
  })
})

describe('Menu', () => {
  test('has Giving', async () => {
    serve({ [ME]: { status: 200, body: ada } })
    open('/menu')

    expect(await screen.findByRole('link', { name: 'Giving' })).toHaveAttribute('href', '/giving')
  })
})
