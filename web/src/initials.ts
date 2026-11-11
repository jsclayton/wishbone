/**
 * The letter an avatar shows for one name: its first letter, with any combining
 * marks that follow it, upper-cased. Anything before the first letter, such as
 * a quote, is skipped. A name with no letter keeps its first character.
 */
function letterOf(name: string): string {
  const chars = Array.from(name.trim())
  if (chars.length === 0) return ''
  let start = chars.findIndex((c) => /\p{L}/u.test(c))
  if (start < 0) start = 0
  let end = start + 1
  while (end < chars.length && /\p{M}/u.test(chars[end]!)) end++
  const [first, ...marks] = chars.slice(start, end)
  return first!.toLocaleUpperCase() + marks.join('')
}

/**
 * What an avatar shows when the user has no photo: the first letter of the first
 * name and of the last name, or only the first letter of the first name when
 * there is no last name. A user summary has no last name, so it gives one letter.
 * Same rule as the server's `initials.Of`.
 */
export function initialsOf(first: string, last?: string | null): string {
  return letterOf(first) + letterOf(last ?? '')
}
