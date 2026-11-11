import type { List, User } from './api'

/** The primary list first, then the others by name. */
export function primaryFirst(lists: List[]): List[] {
  return lists.toSorted((a, b) => Number(b.is_primary) - Number(a.is_primary) || a.name.localeCompare(b.name))
}

/** The lists the user is a recipient of. */
export function ownLists(lists: List[], me: User): List[] {
  return primaryFirst(lists.filter((l) => l.recipients.some((r) => r.id === me.id)))
}

/**
 * The lists the user can edit, in the order of the My lists screen: their own,
 * then a part for each child with the child's primary list first. A list that
 * has both the user and a child as recipients is in the user's part only.
 * `label` names each part.
 */
export function listParts(lists: List[], me: User): { label: string; lists: List[] }[] {
  const parts = [{ label: me.name, lists: ownLists(lists, me) }]
  for (const child of me.children) {
    const theirs = lists.filter((l) => l.recipients.some((r) => r.id === child.id) && !l.recipients.some((r) => r.id === me.id))
    if (theirs.length > 0) parts.push({ label: child.name, lists: primaryFirst(theirs) })
  }
  return parts.filter((p) => p.lists.length > 0)
}
