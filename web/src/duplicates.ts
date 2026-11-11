import type { Item } from './api'

/** A title as the duplicate hint compares it: with extra spaces and case ignored. */
const normalized = (title: string) => title.trim().replace(/\s+/g, ' ').toLocaleLowerCase()

/** Whether two titles are the same, ignoring case and extra spaces. An empty title is the same as nothing. */
export function sameTitle(a: string, b: string): boolean {
  const left = normalized(a)
  return left !== '' && left === normalized(b)
}

/**
 * The first item that the one being added is a duplicate of (docs/spec.md,
 * Items, Screens). An item matches when one of its store links has the address
 * of one of `urls` (the pasted link, as `POST /search` returns it), or when its
 * title is `title`. The hint is the client's own: the server has no duplicate
 * check. An address is the stronger match, so it comes before a title.
 */
export function findDuplicate(items: Item[], title: string, urls: string[]): Item | null {
  return items.find((it) => it.store_links.some((l) => urls.includes(l.url))) ?? items.find((it) => sameTitle(it.title, title)) ?? null
}
