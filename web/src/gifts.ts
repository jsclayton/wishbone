import type { Gift, Item } from './api'
import { shortDate } from './Upcoming'

/**
 * What a giver reads of an item's claim state: "3 of 5 still wanted",
 * or "Always wanted" for an unlimited item. `null` for a recipient, whose item
 * has no claim state (docs/spec.md, Lists, Screens).
 */
export function stillWantedText(item: Pick<Item, 'quantity' | 'still_wanted'>): string | null {
  if (item.still_wanted === undefined) return null
  return item.still_wanted === null ? 'Always wanted' : `${item.still_wanted} of ${item.quantity} still wanted`
}

/** "Mom", "Mom and Dad", "Mom, Dad and Gran": names read as a sentence does. */
export function joinNames(names: string[]): string {
  return names.length < 2 ? (names[0] ?? '') : `${names.slice(0, -1).join(', ')} and ${names.at(-1)}`
}

/** The day after an ISO date: the day a gift for an occasion on it is given. */
function nextDay(iso: string): string {
  const [y = 1970, m = 1, d = 1] = iso.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, d + 1)).toISOString().slice(0, 10)
}

/** "Mar 4": the day the gift is given, which is all a giver sees of an occasion they may not see. */
export function revealDay(gift: Pick<Gift, 'occasion_date'>): string {
  return shortDate(nextDay(gift.occasion_date))
}

/**
 * Whose birthday a gift's birthday occasion is: the owner that was looked up
 * (`owners`, by occasion ID), or the one recipient of the list. `null` when
 * neither is known.
 */
export function birthdayOf(gift: Pick<Gift, 'occasion' | 'recipients'>, owners?: Map<string, string>): string | null {
  const o = gift.occasion
  if (!o || o.kind !== 'birthday') return null
  const names = gift.recipients ?? []
  return owners?.get(o.id) ?? (names.length === 1 ? names[0]!.name : null)
}

/**
 * The occasion of a gift in words: its name, or "Mom's birthday" for a birthday
 * whose user is known, and "a birthday" when it is not. `null` when the occasion
 * is hidden from the giver.
 */
export function occasionText(gift: Pick<Gift, 'occasion' | 'recipients'>, owners?: Map<string, string>): string | null {
  const o = gift.occasion
  if (!o) return null
  if (o.kind !== 'birthday') return o.name
  const whose = birthdayOf(gift, owners)
  return whose ? `${whose}'s birthday` : 'a birthday'
}

/** "You claimed 1 for Christmas", or "You claimed 1, hidden until Mar 4" when the occasion is hidden. */
export function claimedText(gift: Gift, owners?: Map<string, string>): string {
  const occasion = occasionText(gift, owners)
  return occasion ? `You claimed ${gift.quantity} for ${occasion}` : `You claimed ${gift.quantity}, hidden until ${revealDay(gift)}`
}
