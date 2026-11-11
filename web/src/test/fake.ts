import { render, screen } from '@testing-library/react'
import { createElement } from 'react'
import { App } from '../App'
import type { Collection, Gift, Group, Item, List, User } from '../api'

/** Ada, the admin, who has confirmed her profile. */
export const ada: User = {
  id: '7',
  name: 'Ada',
  last_name: 'Lovelace',
  avatar: null,
  birthday: { month: 12, day: 10, year: null },
  good_to_know: null,
  timezone: null,
  is_admin: true,
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
  primary_list: { id: '11', name: 'Ada' },
}

/** Grace, as the admin sees her: another user, whom the admin can edit but who confirms for herself. */
export const grace: User = {
  ...ada,
  id: '8',
  name: 'Grace',
  last_name: 'Hopper',
  birthday: { month: 12, day: 9, year: 1906 },
  is_admin: false,
  profile_confirmed_at: null,
  can_confirm_profile: false,
  can_edit_parents: true,
  can_deactivate: true,
  // The admin's access gives no occasion of Grace's to add.
  can_add_occasion: false,
  // The admin doesn't see Grace's primary list.
  primary_list: undefined,
}

/** Ada's primary list, as she sees it. */
export const adaList: List = {
  id: '11',
  name: 'Ada',
  description: null,
  is_primary: true,
  recipients: [{ id: '7', name: 'Ada', avatar: null }],
  item_count: 0,
  created_at: '2026-11-08T12:00:00Z',
  can_edit: true,
  can_delete: false,
  can_invite: false,
  can_leave: false,
}

/** A kettle, the first item on Ada's list. */
export const kettle: Item = {
  id: '21',
  list_id: '11',
  title: 'A kettle',
  notes: null,
  price_minor: null,
  top_price_minor: null,
  currency: null,
  quantity: 1,
  is_unlimited: false,
  position: 0,
  added_by: { id: '7', name: 'Ada', avatar: null },
  created_at: '2026-11-08T12:30:00Z',
  store_links: [],
  images: [],
  primary_image: null,
  can_edit: true,
  can_delete: true,
  can_claim: false,
  can_refresh_prices: false,
}

/** A gift of Ada's, on a kettle on Mom's list (list 12), for Mom's First Advent. */
export const aGift: Gift = {
  id: '60',
  state: 'open',
  quantity: 1,
  item: { id: '21', title: 'A kettle', primary_image: null },
  list: { id: '12', name: 'Mom' },
  title: 'A kettle',
  image: null,
  price_minor: null,
  top_price_minor: null,
  currency: null,
  recipients: [{ id: '9', name: 'Mom', avatar: null }],
  giver: { id: '7', name: 'Ada', avatar: null },
  giver_name: 'Ada',
  from_label: null,
  occasion: { id: '31', kind: 'custom', name: 'First Advent' },
  occasion_name: 'First Advent',
  occasion_date: '2026-12-06',
  reveal_at: '2026-12-07T00:00:00Z',
  given_at: null,
  is_bought: false,
  note: null,
  flags: [],
  needs_attention: false,
  created_at: '2026-11-08T12:40:00Z',
  can_edit: true,
  can_delete: true,
  can_raise_quantity: true,
  can_acknowledge: false,
  can_restore: false,
  can_add_again: false,
}

/** My family, as the admin sees it: a group the admin manages. */
export const family: Group = {
  id: '5',
  name: 'My family',
  created_at: '2026-11-08T12:00:00Z',
  through: null,
  can_edit: true,
  can_delete: true,
  can_add_user: true,
  can_add_group: true,
}

export const problem = (status: number, code: string, detail = 'words') => ({ status, body: { status, code, detail } })

export const page = <T>(data: T[], next_cursor: string | null = null) => ({
  status: 200,
  body: { data, next_cursor, total_count: data.length } satisfies Collection<T>,
})

/** A fake server: each route answers with a status and a JSON body. A route that is not listed fails the test. */
export type Answer = { status: number; body?: unknown; headers?: Record<string, string> }
export function serve(routes: Record<string, Answer | ((init?: RequestInit) => Answer)>) {
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const key = `${init?.method ?? 'GET'} ${String(input)}`
    // Home's Coming up asks for the next 60 days. A test that is not about it gets none.
    const route: Answer | ((init?: RequestInit) => Answer) | undefined =
      routes[key] ?? (key.startsWith('GET /api/v2/occasions?next_date_before=') ? page([]) : undefined)
    if (!route) throw new Error(`no fake for ${key}`)
    const { status, body, headers } = typeof route === 'function' ? route(init) : route
    return new Response(body === undefined ? null : JSON.stringify(body), { status, headers })
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

export function open(path: string) {
  window.history.replaceState(null, '', path)
  return render(createElement(App))
}

export const heading = (name: string) => screen.findByRole('heading', { level: 1, name })

export const ME = 'GET /api/v2/users/me'

/** The body that a request sent, as an object. */
export function sentBody(fetch: ReturnType<typeof serve>, key: string): unknown {
  const call = fetch.mock.calls.find(([input, init]) => `${init?.method ?? 'GET'} ${String(input)}` === key)
  if (!call) throw new Error(`${key} was not called`)
  return JSON.parse(String(call[1]?.body))
}
