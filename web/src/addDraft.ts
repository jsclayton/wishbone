import type { SearchResult } from './api'
import { openScreen } from './navigation'

/**
 * What the add form opens with: a search's result, and the photo if the input was
 * one. Nothing is saved until the user adds the item (docs/spec.md, Search).
 */
export type Draft = {
  /** What the input was, which says what the form tells the user. */
  kind: 'words' | 'link' | 'photo'
  result: SearchResult
  /** True when the hourly limit stopped the search: the link or photo was not read. */
  limited: boolean
  /** The photo, kept in the browser until the item is saved. `url` is a data: URL for the preview. */
  photo: { file: File; url: string } | null
  /** The day the search was made, as the temporary title of a photo says it: "Nov 1". */
  day: string
}

/** The day as a photo's temporary title says it: "Nov 1". */
export const photoDay = (when = new Date()) => when.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })

export const photoTitle = (day: string) => `Photo from ${day}`

/**
 * Whether one token of pasted text is a web address: it has an http or https
 * scheme, starts with "www.", or is a host with a path such as
 * "shop.example/kettle". The server decides the same way.
 */
export const looksLikeLink = (text: string) => /^(https?:\/\/\S+|www\.\S+|([a-z0-9-]+\.)+[a-z]{2,}\/\S*)$/i.test(text.trim())

/** The store of a link as typed: its host with no `www.`. Empty when the text is not an address. */
export function storeOf(link: string): string {
  try {
    return new URL(link.includes('://') ? link : `https://${link}`).hostname.replace(/^www\./, '')
  } catch {
    return ''
  }
}

const MAX_TITLE = 300
const MAX_NOTES = 2000
const cut = (text: string, max: number) => [...text].slice(0, max).join('')

/** What a search gives when it reads nothing: only what the input gave. */
export function unread(typed: string | null): SearchResult {
  const nothing: SearchResult = {
    title: null,
    notes: null,
    notes_cut: false,
    price_minor: null,
    top_price_minor: null,
    currency: null,
    store_links: [],
    image_urls: [],
    options: [],
    found: false,
  }
  if (typed === null) return nothing
  if (looksLikeLink(typed)) {
    return { ...nothing, store_links: [{ url: typed.trim(), store: storeOf(typed), price_minor: null, currency: null, image_url: null }] }
  }
  // As the search does: the title is cut to the longest title, the notes to the longest notes.
  const words = typed.trim()
  return { ...nothing, title: cut(words, MAX_TITLE), notes: cut(words, MAX_NOTES), notes_cut: [...words].length > MAX_NOTES }
}

/** Reads the photo into a `data:` URL for the preview. Rejects if the browser can't read it. */
export function readPhoto(file: File): Promise<{ file: File; url: string }> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve({ file, url: String(reader.result) })
    reader.onerror = () => reject(reader.error ?? new Error('read failed'))
    reader.readAsDataURL(file)
  })
}

// A File can't live in a history entry, so the drafts are held here and the
// entry holds a key. A reload drops them, and the add form then opens blank.
const drafts = new Map<string, Draft>()
let counter = 0

/** Opens the add form of a list with a draft in it. Back returns to the screen that started it. */
export function openAddForm(listId: string, draft: Draft) {
  const key = `${Date.now()}-${counter++}`
  drafts.set(key, draft)
  openScreen(`/lists/${listId}/add`, { draft: key })
}

/** The draft that the current history entry holds, or null for an add form that was opened plain. */
export function readDraft(): Draft | null {
  const key = (window.history.state as { draft?: string } | null)?.draft
  return key ? (drafts.get(key) ?? null) : null
}
