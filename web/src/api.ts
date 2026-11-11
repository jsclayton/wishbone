import { LONG_TIMEOUT_MS, request } from './http'

/** Every call goes under this. The routes in docs/routes.md are relative to it. */
const api = '/api/v2'

/** A date that can have no year, such as a birthday. */
export type PartialDate = { month: number; day: number; year: number | null }

/** An image as another resource contains it. `url` is the address of its bytes. */
export type ImageSummary = { id: string; url: string; width: number; height: number }

/** A full user, as `GET /users/{id}` returns it. */
export type User = {
  /** An opaque string. Don't parse it. */
  id: string
  name: string
  last_name: string | null
  /** The user's photo, or null when there is none: the app shows their initials. */
  avatar: ImageSummary | null
  birthday: PartialDate
  good_to_know: string | null
  /** An IANA zone name. The profile screen has no field for it. Absent unless the caller is the user or the admin. */
  timezone?: string | null
  is_admin: boolean
  /** Absent unless the caller is the user or the admin. */
  profile_confirmed_at?: string | null
  /**
   * When the admin deactivated the user, or `null` for an active user. Absent
   * unless the caller is the user, a parent of the user, or the admin, who are
   * also the only callers who see a deactivated user.
   */
  deactivated_at?: string | null
  created_at: string
  /** What the caller can do with this user. They differ from caller to caller. */
  can_edit: boolean
  can_make_login_link: boolean
  can_manage_sessions: boolean
  can_confirm_profile: boolean
  /** True only for the admin, for a user who is not the admin. */
  can_edit_parents: boolean
  /** True only for the admin, for an active user who is not the admin. Deactivating opens a confirmation screen. */
  can_deactivate: boolean
  /** True only for the admin, for a deactivated user. */
  can_reactivate: boolean
  /** True when the caller is the user or their parent and the user is active. The admin's access doesn't count. */
  can_add_occasion: boolean
  /** The user's parents and children, by name. Anyone who sees the user gets them. */
  parents: UserSummary[]
  children: UserSummary[]
  /** The user's primary list. Absent unless the caller can see that list. The admin does not, through the admin's access alone. */
  primary_list?: ListSummary
}

/** A small reference to a list, as another resource holds it. */
export type ListSummary = { id: string; name: string }

/** A small reference to a user, as another resource holds it. */
export type UserSummary = { id: string; name: string; avatar: ImageSummary | null }

/** Whether the user is deactivated, for a caller who gets `deactivated_at`. */
export function isDeactivated(user: Pick<User, 'deactivated_at'>): boolean {
  return Boolean(user.deactivated_at)
}

/** The user's name as the app shows it: the first name and the last name, if any. */
export function fullName(user: Pick<User, 'name' | 'last_name'>): string {
  return [user.name, user.last_name].filter(Boolean).join(' ')
}

/** The body of every 4xx and 5xx answer: RFC 9457 problem+json with a `code`. */
type Problem = { code?: string; detail?: string }

async function problemOf(res: Response): Promise<Problem> {
  return (await res.json().catch(() => ({}))) as Problem
}

/** Returns the signed-in user, or null when the browser has no valid session. */
export async function getMe(): Promise<User | null> {
  const res = await request(`${api}/users/me`)
  if (res.status === 401) return null
  if (!res.ok) throw new Error(`GET /users/me failed: ${res.status}`)
  return res.json()
}

/** Returns a user the caller can see, or null when there is none. */
export async function getUser(userId: string): Promise<User | null> {
  const res = await request(`${api}/users/${userId}`)
  if (res.status === 404) return null
  if (!res.ok) throw new Error(`GET /users/{id} failed: ${res.status}`)
  return res.json()
}

/** The version of the running server, such as `2.1.0+abc1234`. Throws when the server can't say. */
export async function getVersion(): Promise<string> {
  const res = await request(`${api}/health`)
  if (!res.ok) throw new Error(`GET /health failed: ${res.status}`)
  return (await res.json()).version
}

/** Whether the instance has no users yet. */
export async function getSetup(): Promise<boolean> {
  const res = await request(`${api}/setup`)
  if (!res.ok) throw new Error(`GET /setup failed: ${res.status}`)
  return (await res.json()).is_needed
}

export type SetupInput = {
  setup_code: string
  name: string
  last_name?: string
  birthday: PartialDate
}

/** What can go wrong with a setup that the person can fix or must know about. */
export type SetupProblem = 'code' | 'complete' | 'rate_limited' | 'invalid'

export type SetupResult = { ok: true; user: User } | { ok: false; problem: SetupProblem; detail: string }

const setupProblems: Record<string, SetupProblem> = {
  setup_code_invalid: 'code',
  setup_complete: 'complete',
  rate_limited: 'rate_limited',
  invalid_input: 'invalid',
}

/**
 * Creates the first user, the admin, and starts the cookie session. A problem
 * the person can act on comes back as a result. Anything else throws.
 */
export async function postSetup(input: SetupInput): Promise<SetupResult> {
  const res = await request(`${api}/setup`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  if (res.ok) return { ok: true, user: await res.json() }
  const body = await problemOf(res)
  const problem = body.code ? setupProblems[body.code] : undefined
  if (!problem) throw new Error(`POST /setup failed: ${res.status}`)
  return { ok: false, problem, detail: body.detail ?? '' }
}

/** Ends the session this browser uses. A browser that is already signed out is done. */
export async function logOut(): Promise<void> {
  const res = await request(`${api}/users/me/sessions/current`, { method: 'DELETE' })
  if (!res.ok && res.status !== 401) throw new Error(`log out failed: ${res.status}`)
}

/** Thrown when the server rate-limits a sign-in attempt. */
export class RateLimitedError extends Error {}

/** What a login link's page may know before the link is redeemed. */
export type LinkCheck = { expires_at: string; for_current_user: boolean }

/**
 * Checks a login link without redeeming it. Returns null for a link that doesn't
 * work: a 401 here is a bad link, not a lost session. The token goes in the
 * body, to stay out of logs.
 */
export async function checkLoginLink(token: string): Promise<LinkCheck | null> {
  const res = await request(`${api}/login-links/check`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  })
  if (res.status === 401) return null
  if (res.status === 429) throw new RateLimitedError('Too many attempts. Wait a minute and try again.')
  if (!res.ok) throw new Error(`check failed: ${res.status}`)
  return res.json()
}

/** What redeeming a login link tells the page. The web app never asks for a bearer token. */
export type Redeemed = { name: string; initials: string; replaced_session: boolean; switched_user: boolean }

/** Redeems a login link and signs this browser in with the cookie. Returns null when the link doesn't work. */
export async function redeemLoginLink(token: string): Promise<Redeemed | null> {
  const res = await request(`${api}/login-links/redeem`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token, session: 'cookie' }),
  })
  if (res.status === 401) return null
  if (res.status === 429) throw new RateLimitedError('Too many attempts. Wait a minute and try again.')
  if (!res.ok) throw new Error(`redeem failed: ${res.status}`)
  return res.json()
}

/** A login link the server just made. Its URL is in this answer and nowhere else. */
export type LoginLinkMade = { url: string; expires_at: string }

/** Makes a login link for a user. */
export async function makeLoginLink(userId: string): Promise<LoginLinkMade> {
  const res = await request(`${api}/users/${userId}/login-links`, { method: 'POST' })
  if (!res.ok) throw new Error(`make a login link failed: ${res.status}`)
  return res.json()
}

/** A signed-in browser or app of a user. */
export type Session = {
  id: string
  user_agent: string
  last_used_at: string
  created_at: string
  is_current: boolean
  can_delete: boolean
}

/** Every collection answers in this envelope. */
export type Collection<T> = { data: T[]; next_cursor: string | null; total_count: number }

/** The address of the next page of a collection, from the cursor that the last page gave. */
export function pageAfter(path: string, cursor: string): string {
  const url = new URL(path, window.location.origin)
  url.searchParams.set('cursor', cursor)
  return url.pathname + url.search
}

/** What a write that the person can fix comes back as. Anything else throws. */
export type Written<T> = { ok: true; value: T } | { ok: false; detail: string }

async function written<T>(res: Response, what: string, alsoRefused: number[] = []): Promise<Written<T>> {
  if (res.ok) return { ok: true, value: await res.json() }
  const body = await problemOf(res)
  if ((res.status === 422 || res.status === 413 || alsoRefused.includes(res.status)) && body.detail) return { ok: false, detail: body.detail }
  throw new Error(`${what} failed: ${res.status}`)
}

export type NewUserInput = { name: string; last_name?: string; birthday: PartialDate; parent_ids?: string[] }

/** Makes a user. Only the admin can. */
export async function createUser(input: NewUserInput): Promise<Written<User>> {
  const res = await request(`${api}/users`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  return written(res, 'POST /users')
}

/** The fields of a profile that can change. A field that is left out stays. `null` clears the last name and the note. */
export type UserChanges = {
  name?: string
  last_name?: string | null
  birthday?: PartialDate
  good_to_know?: string | null
  /** The ID of an uploaded image to use as the photo. `null` removes the photo. */
  avatar_image_id?: string | null
  /** Only the admin sets the parents. The list replaces them: an empty one removes them all. */
  parent_ids?: string[]
}

/** Edits a user's profile with a merge patch. It never confirms the profile. */
export async function updateUser(userId: string, changes: UserChanges): Promise<Written<User>> {
  const res = await request(`${api}/users/${userId}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/merge-patch+json' },
    body: JSON.stringify(changes),
  })
  return written(res, 'PATCH /users/{id}')
}

/**
 * Tells the server the signed-in user's timezone. It is silent: a
 * refusal, a server error and a dropped connection all end with nothing, because
 * the report is not worth a message, and the next visit sends it again.
 */
export async function reportTimezone(timezone: string): Promise<void> {
  try {
    await request(`${api}/users/me`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json' },
      body: JSON.stringify({ timezone }),
    })
  } catch {
    // Offline or a timeout. Try again next visit.
  }
}

/** Replaces a user's parents. Only the admin can. A refusal, such as a loop, comes back as a result. */
export function setParents(userId: string, parentIds: string[]): Promise<Written<User>> {
  return updateUser(userId, { parent_ids: parentIds })
}

/** The kinds of file the photo picker offers. The server decides what it accepts. */
export const PHOTO_TYPES = 'image/jpeg,image/png,image/gif,image/webp'

/** An image that was just uploaded. It is attached to nothing until a change names its ID. */
export type UploadedImage = ImageSummary & { content_type: string }

/**
 * Uploads a file as it is: the server decodes it, shrinks it, and strips its
 * metadata. A file the server refuses comes back as a result. The call gets the
 * long timeout, because the file can be 10 MB. The body is the file's bytes and
 * not the `File`, which WebKit's test driver can't read back from a request.
 */
export async function uploadImage(file: Blob): Promise<Written<UploadedImage>> {
  const res = await request(
    `${api}/images`,
    { method: 'POST', headers: { 'Content-Type': file.type || 'application/octet-stream' }, body: await file.arrayBuffer() },
    LONG_TIMEOUT_MS,
  )
  return written(res, 'POST /images')
}

/**
 * Has the server download an image from a web address, as it would an upload.
 * Every failure of the download comes back as a refusal: the web app then leaves
 * that image out. The call gets the long timeout, because the shop may be slow.
 */
export async function downloadImage(url: string): Promise<Written<UploadedImage>> {
  const res = await request(
    `${api}/images`,
    { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ url }) },
    LONG_TIMEOUT_MS,
  )
  return written(res, 'POST /images')
}

/** What a search suggests for an item. Its fields have the names of the `POST /lists/{id}/items` body. */
export type SearchResult = {
  title: string | null
  /** For free text, the text as typed. */
  notes: string | null
  /** True when the free text was longer than the longest notes. */
  notes_cut: boolean
  price_minor: number | null
  top_price_minor: number | null
  currency: string | null
  /** For a link, one store link with the page's price. */
  store_links: { url: string; store: string; price_minor: number | null; currency: string | null; image_url: string | null }[]
  /** Up to 5 of the page's images, best first. A picked one is saved with `downloadImage`. */
  image_urls: string[]
  options: { name: string; values: string[]; selected: string | null }[]
  /** False when nothing was read from the input. */
  found: boolean
}

/**
 * What a search came back as. `limited` is the hourly limit: the link or photo
 * was not read. `blocked` is a link from a blocked store. `refused` is anything
 * else the person can fix, such as a file that is not an image. Anything else
 * throws.
 */
export type Searched =
  | { ok: true; result: SearchResult }
  | { ok: false; reason: 'limited' | 'blocked' | 'refused'; detail: string }

/** A multipart/form-data body, built by hand: the bytes of a file go in as they are, so every browser sends the same body. */
async function multipart(fields: Record<string, string>, files: Record<string, Blob>): Promise<{ body: ArrayBuffer; contentType: string }> {
  // getRandomValues works on a page that is not a secure context, such as http on a LAN.
  const boundary = `----wishbone${Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => b.toString(16).padStart(2, '0')).join('')}`
  const enc = new TextEncoder()
  const parts: BlobPart[] = []
  for (const [name, value] of Object.entries(fields)) {
    parts.push(enc.encode(`--${boundary}\r\nContent-Disposition: form-data; name="${name}"\r\n\r\n`), enc.encode(value), enc.encode('\r\n'))
  }
  for (const [name, file] of Object.entries(files)) {
    parts.push(
      enc.encode(`--${boundary}\r\nContent-Disposition: form-data; name="${name}"; filename="photo"\r\nContent-Type: ${file.type || 'application/octet-stream'}\r\n\r\n`),
      await file.arrayBuffer(),
      enc.encode('\r\n'),
    )
  }
  parts.push(enc.encode(`--${boundary}--\r\n`))
  return { body: await new Blob(parts).arrayBuffer(), contentType: `multipart/form-data; boundary=${boundary}` }
}

/**
 * Turns a link, typed words, or a photo into suggested item details. Nothing is
 * saved. A link can take up to 60 s to read, so the call gets the long timeout;
 * `signal` ends the wait sooner, and the call then rejects.
 */
export async function search(input: { text: string } | { image: Blob }, signal?: AbortSignal): Promise<Searched> {
  const { body, contentType } = 'text' in input ? await multipart({ text: input.text }, {}) : await multipart({}, { image: input.image })
  const res = await request(`${api}/search`, { method: 'POST', headers: { 'Content-Type': contentType }, body, signal }, LONG_TIMEOUT_MS)
  if (res.ok) return { ok: true, result: ((await res.json()) as { results: SearchResult[] }).results[0]! }
  const problem = await problemOf(res)
  const detail = problem.detail ?? ''
  if (res.status === 429) return { ok: false, reason: 'limited', detail }
  if (res.status === 422 && problem.code === 'store_blocked') return { ok: false, reason: 'blocked', detail }
  if ((res.status === 422 || res.status === 413) && detail) return { ok: false, reason: 'refused', detail }
  throw new Error(`POST /search failed: ${res.status}`)
}

/** Uploads a file, then makes it the user's photo. The user it returns has the new photo. */
export async function setAvatar(userId: string, file: Blob): Promise<Written<User>> {
  const image = await uploadImage(file)
  if (!image.ok) return image
  return updateUser(userId, { avatar_image_id: image.value.id })
}

/** Removes the user's photo. The server deletes the image. */
export function removeAvatar(userId: string): Promise<Written<User>> {
  return updateUser(userId, { avatar_image_id: null })
}

/** The signed-in user confirms their own profile. */
export async function confirmProfile(userId: string): Promise<void> {
  const res = await request(`${api}/users/${userId}/confirm-profile`, { method: 'POST' })
  if (!res.ok) throw new Error(`confirm profile failed: ${res.status}`)
}

/** Deactivates a user. Only the admin can. The user it returns is deactivated. */
export async function deactivateUser(userId: string): Promise<User> {
  const res = await request(`${api}/users/${userId}/deactivate`, { method: 'POST' })
  if (!res.ok) throw new Error(`deactivate failed: ${res.status}`)
  return res.json()
}

/** Reactivates a user. Only the admin can. The user it returns is active again, as they were. */
export async function reactivateUser(userId: string): Promise<User> {
  const res = await request(`${api}/users/${userId}/reactivate`, { method: 'POST' })
  if (!res.ok) throw new Error(`reactivate failed: ${res.status}`)
  return res.json()
}

/** Revokes one of a user's sessions. A session that is already gone is done. */
export async function revokeSession(userId: string, sessionId: string): Promise<void> {
  const res = await request(`${api}/users/${userId}/sessions/${sessionId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`revoke session failed: ${res.status}`)
}

/** A passkey of a user: a credential that signs them in without a login link. */
export type Passkey = {
  id: string
  name: string
  created_at: string
  /** `null` until the passkey signs in. */
  last_used_at: string | null
  /** Only the user renames a passkey. */
  can_edit: boolean
  can_delete: boolean
  version: string
}

/** Renames a passkey with a merge patch. A name the server refuses comes back as a result. */
export async function renamePasskey(userId: string, passkeyId: string, name: string): Promise<Written<Passkey>> {
  const res = await request(`${api}/users/${userId}/passkeys/${passkeyId}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/merge-patch+json' },
    body: JSON.stringify({ name }),
  })
  return written(res, 'PATCH /users/{id}/passkeys/{passkey_id}')
}

/** Deletes one of a user's passkeys. A passkey that is already gone is done. */
export async function deletePasskey(userId: string, passkeyId: string): Promise<void> {
  const res = await request(`${api}/users/${userId}/passkeys/${passkeyId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`delete passkey failed: ${res.status}`)
}

/** A group, as `GET /groups/{id}` returns it. */
export type Group = {
  id: string
  name: string
  created_at: string
  /** The group the caller is directly in, and that this group contains by nesting. `null` when the caller is in this group directly, or not in it. */
  through: GroupSummary | null
  /** Each is true only for the admin. */
  can_edit: boolean
  can_delete: boolean
  can_add_user: boolean
  can_add_group: boolean
}

/** A small reference to a group, as another resource holds it. */
export type GroupSummary = { id: string; name: string }

/** Loads every page of a collection and returns its rows. A page that fails throws. */
export async function fetchAll<T>(path: string): Promise<T[]> {
  const rows: T[] = []
  let next: string | null = path
  while (next) {
    const res: Response = await request(next)
    if (!res.ok) throw new Error(`GET ${next} failed: ${res.status}`)
    const body: Collection<T> = await res.json()
    rows.push(...body.data)
    next = body.next_cursor ? pageAfter(path, body.next_cursor) : null
  }
  return rows
}

const json = { 'Content-Type': 'application/json' }

/** Makes a group. Only the admin can. */
export async function createGroup(name: string): Promise<Written<Group>> {
  const res = await request(`${api}/groups`, { method: 'POST', headers: json, body: JSON.stringify({ name }) })
  return written(res, 'POST /groups')
}

/** Renames a group with a merge patch. */
export async function renameGroup(groupId: string, name: string): Promise<Written<Group>> {
  const res = await request(`${api}/groups/${groupId}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/merge-patch+json' },
    body: JSON.stringify({ name }),
  })
  return written(res, 'PATCH /groups/{id}')
}

/** Deletes a group. A group that is already gone is done. */
export async function deleteGroup(groupId: string): Promise<void> {
  const res = await request(`${api}/groups/${groupId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`delete group failed: ${res.status}`)
}

/** Puts a user directly in a group. A refusal comes back as a result. */
export async function addUserToGroup(groupId: string, userId: string): Promise<Written<User>> {
  const res = await request(`${api}/groups/${groupId}/users`, { method: 'POST', headers: json, body: JSON.stringify({ user_id: userId }) })
  return written(res, 'POST /groups/{id}/users', [409])
}

/** Takes a user out of a group. A user who is already out is done. */
export async function removeUserFromGroup(groupId: string, userId: string): Promise<void> {
  const res = await request(`${api}/groups/${groupId}/users/${userId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`remove user from group failed: ${res.status}`)
}

/** What nesting a group can come back as: done, a loop (the group already holds the other), or another refusal. */
export type Nested = { ok: true } | { ok: false; loop: boolean; detail: string }

/** Puts a group directly inside another. */
export async function addGroupToGroup(outerId: string, innerId: string): Promise<Nested> {
  const res = await request(`${api}/groups/${outerId}/groups`, { method: 'POST', headers: json, body: JSON.stringify({ group_id: innerId }) })
  if (res.ok) return { ok: true }
  const body = await problemOf(res)
  if ((res.status === 422 || res.status === 409) && body.detail) return { ok: false, loop: body.code === 'group_loop', detail: body.detail }
  throw new Error(`POST /groups/{id}/groups failed: ${res.status}`)
}

/** Takes a group out of another. A group that is already out is done. */
export async function removeGroupFromGroup(outerId: string, innerId: string): Promise<void> {
  const res = await request(`${api}/groups/${outerId}/groups/${innerId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`remove group from group failed: ${res.status}`)
}

/** A store the admin has blocked, as `GET /blocked-stores` returns it. */
export type BlockedStore = {
  id: string
  /** A lowercase host such as `temu.com`. It matches the host and its subdomains. */
  store: string
  created_at: string
  /** True for the admin, the only caller who sees one. */
  can_delete: boolean
}

/**
 * Blocks a store: a host name or a web address. A store that is already blocked
 * is done, as the API says a client can treat it. A refusal that the admin can
 * fix comes back as a result.
 */
export async function blockStore(store: string): Promise<Written<null>> {
  const res = await request(`${api}/blocked-stores`, { method: 'POST', headers: json, body: JSON.stringify({ store }) })
  if (res.ok || res.status === 409) return { ok: true, value: null }
  return written(res, 'POST /blocked-stores')
}

/** Allows a store again. A store that is already allowed is done. */
export async function allowStore(storeId: string): Promise<void> {
  const res = await request(`${api}/blocked-stores/${storeId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`allow store failed: ${res.status}`)
}

/** A full list, as `GET /lists/{id}` returns it. */
export type List = {
  id: string
  name: string
  /** Markdown, or null. */
  description: string | null
  /** A primary list is the one every user has, with that user as its only recipient. */
  is_primary: boolean
  recipients: UserSummary[]
  /** How many items the caller can see on the list. */
  item_count: number
  created_at: string
  /** What the caller can do with this list. */
  can_edit: boolean
  can_delete: boolean
  can_invite: boolean
  can_leave: boolean
}

/** An item, as `GET /items/{id}` returns it. A recipient gets no `still_wanted` and no `my_claimed_quantity`. */
export type Item = {
  id: string
  list_id: string
  title: string
  notes: string | null
  /** In the currency's minor unit. */
  price_minor: number | null
  /** The top of a range that starts at `price_minor`. */
  top_price_minor: number | null
  /** An ISO 4217 code. `null` exactly when there is no price. */
  currency: string | null
  quantity: number
  is_unlimited: boolean
  /** A 0-based index on the list. */
  position: number
  /** Who added the item, or null once that user is deleted. */
  added_by: UserSummary | null
  created_at: string
  /** At most 10. The users who can edit the list get them in the order added, everyone else by price. */
  store_links: StoreLink[]
  /** At most 5, in the order they were added. */
  images: ImageSummary[]
  /** One of `images`, or null when there are none. */
  primary_image: ImageSummary | null
  /** A giver's: how many other givers can still claim, or `null` for an unlimited item. */
  still_wanted?: number | null
  /** A giver's: the sum of the caller's own open gifts on the item. */
  my_claimed_quantity?: number
  can_edit: boolean
  can_delete: boolean
  can_claim: boolean
  /** The same as `can_edit`, and the item has a store link. */
  can_refresh_prices: boolean
}

/** The most store links an item can have. The server enforces it too. */
export const MAX_STORE_LINKS = 10

/** The most images an item can have. The server enforces it too. */
export const MAX_IMAGES = 5

/** One place where an item can be bought. */
export type StoreLink = {
  id: string
  url: string
  /** The host, with no `www.`. */
  store: string
  /** In the currency's minor unit. */
  price_minor: number | null
  currency: string | null
  captured_at: string | null
  /** The store's own picture. It is outside Wishbone: the browser loads it directly. */
  image_url: string | null
  created_at: string
  can_edit: boolean
  can_delete: boolean
}

/** A store link to add with a new item. */
export type NewStoreLink = {
  url: string
  price_minor?: number | null
  currency?: string | null
  image_url?: string | null
}

/** What a new item takes. A field that is left out has the server's default. */
export type NewItem = {
  title: string
  notes?: string | null
  price_minor?: number | null
  top_price_minor?: number | null
  currency?: string | null
  quantity?: number
  is_unlimited?: boolean
  /** At most 10. */
  store_links?: NewStoreLink[]
  /** IDs of uploaded images, at most 5, in order. */
  image_ids?: string[]
  /** One of `image_ids`. The first by default. */
  primary_image_id?: string | null
}

/** The fields of an item that can change. A field that is left out stays. `null` clears the notes and the price. */
export type ItemChanges = Partial<Omit<NewItem, 'store_links' | 'image_ids' | 'primary_image_id'>> & {
  /** Moves the item to this index on its list, and the others shift. */
  position?: number
  /** Moves the item to the end of this list. The user must be able to edit both lists. */
  list_id?: string
  /** Replaces the whole array of images, in this order. The server deletes any image left out. `[]` removes them all. */
  image_ids?: string[]
  /** One of the images after the change. `null` picks the first. */
  primary_image_id?: string | null
}

const mergePatch = { 'Content-Type': 'application/merge-patch+json' }

/** Adds an item to the end of a list. A refusal the user can fix comes back as a result. */
export async function addItem(listId: string, input: NewItem): Promise<Written<Item>> {
  const res = await request(`${api}/lists/${listId}/items`, { method: 'POST', headers: json, body: JSON.stringify(input) })
  return written(res, 'POST /lists/{id}/items')
}

/** Changes an item with a merge patch. A refusal the user can fix comes back as a result. */
export async function updateItem(itemId: string, changes: ItemChanges): Promise<Written<Item>> {
  const res = await request(`${api}/items/${itemId}`, { method: 'PATCH', headers: mergePatch, body: JSON.stringify(changes) })
  // A 403 has words the user can act on: a move to a list they can't edit.
  return written(res, 'PATCH /items/{id}', [403])
}

/** Deletes an item. An item that is already gone is done. */
export async function deleteItem(itemId: string): Promise<void> {
  const res = await request(`${api}/items/${itemId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`delete item failed: ${res.status}`)
}

/** The fields of a list that can change. A field that is left out stays. `null` or an empty string clears the description. */
export type ListChanges = { name?: string; description?: string | null }

/** Renames a list or changes its description with a merge patch. A refusal the user can fix comes back as a result. */
export async function updateList(listId: string, changes: ListChanges): Promise<Written<List>> {
  const res = await request(`${api}/lists/${listId}`, { method: 'PATCH', headers: mergePatch, body: JSON.stringify(changes) })
  return written(res, 'PATCH /lists/{id}')
}

/** What a new extra list takes. `recipient_id` is the signed-in user (the default) or one of their children. */
export type NewList = { name: string; description?: string | null; recipient_id?: string }

/** Makes an extra list. A refusal the user can fix comes back as a result. */
export async function createList(input: NewList): Promise<Written<List>> {
  const res = await request(`${api}/lists`, { method: 'POST', headers: json, body: JSON.stringify(input) })
  return written(res, 'POST /lists', [403])
}

/** Deletes an extra list and its items. A list that is already gone is done. */
export async function deleteList(listId: string): Promise<void> {
  const res = await request(`${api}/lists/${listId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`delete list failed: ${res.status}`)
}

/** A pending invitation to an extra list, as `GET /invitations` returns it. */
export type Invitation = {
  id: string
  list: ListSummary
  /** The invited user. */
  user: UserSummary
  /** The recipient who invited them, or null once that user is deleted. */
  invited_by: UserSummary | null
  created_at: string
  /** The invited user or their parent, while the invited user is active. */
  can_accept: boolean
  /** A recipient of the list (withdraw), or the invited user or their parent while the invited user is active (decline). */
  can_delete: boolean
}

/** A recipient of a list, as `GET /lists/{id}/recipients` returns it. */
export type Recipient = {
  user: UserSummary
  created_at: string
  /** Whether the user could still see the list after leaving it. Absent unless the caller is that user or their parent. */
  sees_list_after_leaving?: boolean
  /** Whether the caller can make the user leave: the list is an extra list with another recipient, and the caller is the user or their parent. */
  can_delete: boolean
}

/** Invites a user to an extra list. A refusal the user can fix comes back as a result. */
export async function inviteUser(listId: string, userId: string): Promise<Written<Invitation>> {
  const res = await request(`${api}/lists/${listId}/invitations`, { method: 'POST', headers: json, body: JSON.stringify({ user_id: userId }) })
  return written(res, 'POST /lists/{id}/invitations', [403, 409])
}

/** Accepts an invitation, for the invited user or their parent. The answer is the list. */
export async function acceptInvitation(invitationId: string): Promise<Written<List>> {
  const res = await request(`${api}/invitations/${invitationId}/accept`, { method: 'POST' })
  return written(res, 'POST /invitations/{id}/accept', [403, 404])
}

/** Declines or withdraws an invitation. One that is already gone is done. A refusal the person can read comes back as a result. */
export async function deleteInvitation(invitationId: string): Promise<{ ok: true } | { ok: false; detail: string }> {
  const res = await request(`${api}/invitations/${invitationId}`, { method: 'DELETE' })
  if (res.ok || res.status === 404) return { ok: true }
  const body = await problemOf(res)
  if ([403, 409, 412, 422].includes(res.status) && body.detail) return { ok: false, detail: body.detail }
  throw new Error(`delete invitation failed: ${res.status}`)
}

/** How many rows a collection has, from `total_count` of its first page. Put `limit=1` in the path to read no more than one row. */
export async function fetchTotal(path: string): Promise<number> {
  const res = await request(path)
  if (!res.ok) throw new Error(`GET ${path} failed: ${res.status}`)
  const body: Collection<unknown> = await res.json()
  return body.total_count
}

/** Takes a recipient out of a list: they leave, or their parent makes them leave. A recipient who is already gone is done. */
export async function leaveList(listId: string, userId: string): Promise<void> {
  const res = await request(`${api}/lists/${listId}/recipients/${userId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`leave list failed: ${res.status}`)
}

/** An item with its `ETag`, the version to send in `If-Match`. */
export type TaggedItem = { item: Item; etag: string | null }

/** An item with its version, or `null` when there is none. Anything else throws. */
export async function getTaggedItem(itemId: string): Promise<TaggedItem | null> {
  const res = await request(`${api}/items/${itemId}`)
  if (res.status === 404) return null
  if (!res.ok) throw new Error(`GET /items/{id} failed: ${res.status}`)
  return { item: await res.json(), etag: res.headers.get('ETag') }
}

/**
 * What a change that carries `If-Match` came back as. `stale` is a 412: the item
 * changed since it was loaded, and nothing was changed.
 */
export type Checked = ({ ok: true } & TaggedItem) | { ok: false; stale: boolean; detail: string }

/**
 * Changes an item with a merge patch that only applies to the version the user
 * saw (`If-Match`). A refusal the user can fix comes back as a result.
 */
export async function updateItemChecked(itemId: string, changes: ItemChanges, etag: string | null): Promise<Checked> {
  const headers: Record<string, string> = { ...mergePatch }
  if (etag) headers['If-Match'] = etag
  const res = await request(`${api}/items/${itemId}`, { method: 'PATCH', headers, body: JSON.stringify(changes) })
  if (res.ok) return { ok: true, item: await res.json(), etag: res.headers.get('ETag') }
  const body = await problemOf(res)
  if (res.status === 412) return { ok: false, stale: true, detail: body.detail ?? '' }
  if ((res.status === 422 || res.status === 409) && body.detail) return { ok: false, stale: false, detail: body.detail }
  throw new Error(`PATCH /items/{id} failed: ${res.status}`)
}

/** An item the caller can see, or `null` when there is none. Anything else throws. */
export async function getItem(itemId: string): Promise<Item | null> {
  const res = await request(`${api}/items/${itemId}`)
  if (res.status === 404) return null
  if (!res.ok) throw new Error(`GET /items/{id} failed: ${res.status}`)
  return res.json()
}

/**
 * Adds a store link to an item. The server reads the page for its price, which
 * can take up to 60 s and counts against the hourly limit, so the call gets the
 * long timeout. A refusal the user can fix comes back as a result: a link from a
 * blocked store, the hourly limit, or too many links.
 */
export async function addStoreLink(itemId: string, url: string, signal?: AbortSignal): Promise<Written<StoreLink>> {
  const res = await request(
    `${api}/items/${itemId}/store-links`,
    { method: 'POST', headers: json, body: JSON.stringify({ url }), signal },
    LONG_TIMEOUT_MS,
  )
  return written(res, 'POST /items/{id}/store-links', [429])
}

/** The price of a store link, as the user sets it. `null` clears the price. */
export type StoreLinkPrice = { price_minor: number | null; currency?: string }

/** Sets or clears the price of a store link by hand. A refusal the user can fix comes back as a result. */
export async function setStoreLinkPrice(itemId: string, storeLinkId: string, price: StoreLinkPrice): Promise<Written<StoreLink>> {
  const res = await request(`${api}/items/${itemId}/store-links/${storeLinkId}`, { method: 'PATCH', headers: mergePatch, body: JSON.stringify(price) })
  return written(res, 'PATCH /items/{id}/store-links/{store_link_id}')
}

/** Takes a store link off an item. A link that is already gone is done. */
export async function removeStoreLink(itemId: string, storeLinkId: string): Promise<void> {
  const res = await request(`${api}/items/${itemId}/store-links/${storeLinkId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`delete store link failed: ${res.status}`)
}

/**
 * Reads the price of every store link of an item again. It can take up to 75 s
 * and each link counts against the hourly limit, so the call gets the long
 * timeout. The hourly limit before the first read comes back as a result.
 */
export async function refreshPrices(itemId: string): Promise<Written<Item>> {
  const res = await request(`${api}/items/${itemId}/refresh-prices`, { method: 'POST' }, LONG_TIMEOUT_MS)
  return written(res, 'POST /items/{id}/refresh-prices', [429])
}

/** The four date rules (docs/routes.md, Occasion fields). */
export type RuleKind = 'annual' | 'once' | 'nth_weekday' | 'last_weekday'

/** An occasion's date rule, as the server sends it: every field is there, and a field that the kind doesn't use is `null`. `weekday` 0 is Sunday. */
export type Rule = { kind: RuleKind; month: number; year: number | null; day: number | null; weekday: number | null; n: number | null }

/** A date rule to send: the fields that the kind needs, and no others. */
export type RuleInput = { kind: RuleKind; month: number; year?: number; day?: number; weekday?: number; n?: number }

/** An occasion, as `GET /occasions` returns it. */
export type Occasion = {
  id: string
  /** A birthday is made with the user and follows their birthday. A custom occasion is the user's or their parent's. */
  kind: 'birthday' | 'shared' | 'custom'
  name: string
  rule: Rule
  /** The user it is for. `null` for a shared occasion. */
  user: UserSummary | null
  /** For a shared occasion: true when it applies to every user. `null` for the other kinds. */
  is_everyone: boolean | null
  /** YYYY-MM-DD: the first date that is today or later, where the user lives (a shared occasion: where the caller lives). `null` when none is left. */
  next_date: string | null
  /** True for a custom occasion when the caller is its user or their parent, and the user is active, and for a shared occasion when the caller is the admin. */
  can_edit: boolean
  can_delete: boolean
  /** True for a shared occasion that is not marked everyone, when the caller is the admin. */
  can_attach_group: boolean
}

/** What a custom occasion takes: a name and a date rule. */
export type OccasionInput = { name: string; rule: RuleInput }

/** What a shared occasion takes: a name, a date rule, and whether it is everyone. */
export type SharedOccasionInput = OccasionInput & { is_everyone: boolean }

/** Makes a shared occasion, attached to no groups. Only the admin can. */
export async function createSharedOccasion(input: SharedOccasionInput): Promise<Written<Occasion>> {
  const res = await request(`${api}/occasions`, { method: 'POST', headers: json, body: JSON.stringify({ kind: 'shared', ...input }) })
  return written(res, 'POST /occasions', [403])
}

/** Changes a shared occasion with a merge patch. A field that is left out stays. Marking it everyone detaches its groups. */
export async function updateSharedOccasion(occasionId: string, changes: Partial<SharedOccasionInput>): Promise<Written<Occasion>> {
  const res = await request(`${api}/occasions/${occasionId}`, { method: 'PATCH', headers: mergePatch, body: JSON.stringify(changes) })
  return written(res, 'PATCH /occasions/{id}', [403])
}

/** Attaches a group to a shared occasion. A refusal (already attached, or everyone) comes back as a result. */
export async function attachGroup(occasionId: string, groupId: string): Promise<Written<Group>> {
  const res = await request(`${api}/occasions/${occasionId}/groups`, { method: 'POST', headers: json, body: JSON.stringify({ group_id: groupId }) })
  return written(res, 'POST /occasions/{id}/groups', [409])
}

/** Takes a group off a shared occasion. A group that is already off is done. */
export async function detachGroup(occasionId: string, groupId: string): Promise<void> {
  const res = await request(`${api}/occasions/${occasionId}/groups/${groupId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`detach group failed: ${res.status}`)
}

/** Adds a custom occasion for a user: the caller or their child. A refusal the person can fix comes back as a result. */
export async function createOccasion(userId: string, input: OccasionInput): Promise<Written<Occasion>> {
  const res = await request(`${api}/occasions`, { method: 'POST', headers: json, body: JSON.stringify({ kind: 'custom', user_id: userId, ...input }) })
  return written(res, 'POST /occasions', [403])
}

/** Changes a custom occasion's name and date rule with a merge patch. A refusal the person can fix comes back as a result. */
export async function updateOccasion(occasionId: string, input: OccasionInput): Promise<Written<Occasion>> {
  const res = await request(`${api}/occasions/${occasionId}`, { method: 'PATCH', headers: mergePatch, body: JSON.stringify(input) })
  return written(res, 'PATCH /occasions/{id}', [403])
}

/** Deletes a custom occasion. An occasion that is already gone is done. */
export async function deleteOccasion(occasionId: string): Promise<void> {
  const res = await request(`${api}/occasions/${occasionId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`delete occasion failed: ${res.status}`)
}

/** An occasion as a gift holds it. */
export type OccasionSummary = { id: string; kind: 'birthday' | 'shared' | 'custom'; name: string }

/** A price in a flag's diff. */
export type GiftPrice = { price_minor: number; top_price_minor: number | null; currency: string }

/**
 * One line of the diff behind a changed flag: an item field that differs from
 * what the giver picked. `old` and `new` are the field's value, or a
 * {@link GiftPrice} (or `null` for no price) for `price` and `store_link_price`.
 * They are absent for `primary_image` and `store_link_removed`. `url` names a
 * store link.
 */
export type GiftChange = {
  field: 'title' | 'notes' | 'quantity' | 'is_unlimited' | 'price' | 'primary_image' | 'store_link_price' | 'store_link_removed'
  url?: string
  old?: string | number | boolean | GiftPrice | null
  new?: string | number | boolean | GiftPrice | null
}

/** What happened to a gift that its giver should answer. Only the giver sees flags. */
export type GiftFlag =
  | { code: 'changed'; fields: GiftChange[] }
  | { code: 'removed' }
  /** `list` is `null` when the giver can't see the list the item moved to. */
  | { code: 'moved'; list: ListSummary | null }
  /** `old_recipients` and `new_recipients` are `null` when the giver can't see the list. */
  | { code: 'recipients_changed'; old_recipients: UserSummary[] | null; new_recipients: UserSummary[] | null }
  | { code: 'occasion_moved' | 'no_longer_shared' }
  /** `by` is the user who said they didn't get the gift, or `null` when the giver can't see that user. */
  | { code: 'restored'; by: UserSummary | null }

/**
 * A gift, as its giver sees it (`GET /gifts`). Until it is given, nobody else
 * sees it. After reveal its recipients and their parents see it too, as a
 * `ReceivedGift`.
 */
export type Gift = {
  id: string
  state: 'open' | 'given'
  quantity: number
  item: { id: string; title: string; primary_image: ImageSummary | null }
  list: ListSummary
  /** The item's title now. */
  title: string
  image: ImageSummary | null
  price_minor: number | null
  top_price_minor: number | null
  currency: string | null
  /** The list's recipients now, or for a given gift the recipients at reveal. The key is absent when the giver can't see the list (at reveal, for a given gift). */
  recipients?: UserSummary[]
  giver: UserSummary | null
  giver_name: string
  from_label: string | null
  /** `null` when the giver may not see the occasion: only the reveal date shows then. */
  occasion: OccasionSummary | null
  occasion_name: string | null
  /** YYYY-MM-DD. */
  occasion_date: string
  reveal_at: string
  given_at: string | null
  is_bought: boolean
  note: string | null
  /** The giver's open gifts only: a given gift has none. */
  flags?: GiftFlag[]
  /** The giver's open gifts only. */
  needs_attention?: boolean
  created_at: string
  can_edit: boolean
  can_delete: boolean
  can_raise_quantity: boolean
  can_acknowledge: boolean
  /** True for a recipient of a given gift and their parents: "I didn't get this". */
  can_restore: boolean
  /** The same as `can_restore`: add the gift to a list again. */
  can_add_again: boolean
}

/**
 * A given gift as a recipient or a recipient's parent sees it: the gift without
 * the giver's own fields. The keys are absent, not null, so a recipient can't
 * tell whether there is a note.
 */
export type ReceivedGift = Omit<Gift, 'is_bought' | 'note' | 'flags' | 'needs_attention' | 'created_at'> & { state: 'given'; given_at: string }

/** What a claim takes. A field that is left out has the server's default: one, and the first occasion the giver may pick. */
export type NewGift = {
  quantity?: number
  occasion_id?: string
  from_label?: string | null
  note?: string | null
  is_bought?: boolean
}

/** The fields of a gift that its giver can change. A field that is left out stays. `null` clears the from label and the note. */
export type GiftChanges = {
  quantity?: number
  occasion_id?: string
  from_label?: string | null
  note?: string | null
  is_bought?: boolean
}

/** What a claim comes back as. A refusal the person can act on says which. */
export type Claimed =
  | { ok: true; gift: Gift }
  | { ok: false; reason: 'fully_claimed' | 'already_claimed' | 'occasion' | 'other'; detail: string }

/** Claims an item, which starts a gift. Losing the race for the last one is `fully_claimed`. */
export async function claimItem(itemId: string, input: NewGift): Promise<Claimed> {
  const res = await request(`${api}/items/${itemId}/gifts`, { method: 'POST', headers: json, body: JSON.stringify(input) })
  if (res.ok) return { ok: true, gift: await res.json() }
  const body = await problemOf(res)
  const detail = body.detail ?? "That didn't work. Try again."
  if (res.status === 409 && body.code === 'item_fully_claimed') return { ok: false, reason: 'fully_claimed', detail }
  if (res.status === 409 && body.code === 'already_claimed') return { ok: false, reason: 'already_claimed', detail }
  if (res.status === 422 && body.code === 'occasion_not_allowed') return { ok: false, reason: 'occasion', detail }
  if (res.status === 422 || res.status === 403) return { ok: false, reason: 'other', detail }
  throw new Error(`POST /items/{id}/gifts failed: ${res.status}`)
}

/** Changes a gift with a merge patch. A refusal the person can fix comes back as a result. */
export async function updateGift(giftId: string, changes: GiftChanges): Promise<Written<Gift>> {
  const res = await request(`${api}/gifts/${giftId}`, { method: 'PATCH', headers: mergePatch, body: JSON.stringify(changes) })
  return written(res, 'PATCH /gifts/{id}', [403, 409])
}

/** Unclaims a gift. A gift that is already gone is done. */
export async function deleteGift(giftId: string): Promise<void> {
  const res = await request(`${api}/gifts/${giftId}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(`delete gift failed: ${res.status}`)
}

/**
 * "I didn't get this": a recipient of a given gift, or their parent, says it
 * wasn't received. The gift opens again for its giver and leaves Received. The
 * gift is a 404 for the caller afterwards, so a gift that is gone already counts
 * as done.
 */
export async function restoreGift(giftId: string): Promise<void> {
  const res = await request(`${api}/gifts/${giftId}/restore`, { method: 'POST' })
  if (!res.ok && res.status !== 404) throw new Error(`restore gift failed: ${res.status}`)
}

/**
 * Add again: a new item on a list from a given gift, with the gift's title, image
 * (as a new image) and price. A refusal the user can fix comes back as a result.
 */
export async function addItemAgain(listId: string, giftId: string): Promise<Written<Item>> {
  const res = await request(`${api}/lists/${listId}/items`, { method: 'POST', headers: json, body: JSON.stringify({ from_gift_id: giftId }) })
  return written(res, 'POST /lists/{id}/items')
}

/**
 * Answers the flags on a gift: clears the ones named, or every flag when `codes`
 * is left out. Keeping a gift whose item was deleted is clearing `removed`.
 */
export async function acknowledgeGift(giftId: string, codes?: GiftFlag['code'][]): Promise<Written<Gift>> {
  const res = await request(`${api}/gifts/${giftId}/acknowledge`, { method: 'POST', headers: json, body: JSON.stringify(codes ? { codes } : {}) })
  return written(res, 'POST /gifts/{id}/acknowledge', [403])
}
