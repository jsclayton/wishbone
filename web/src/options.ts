/** One choice a product page offers, as a search result gives it. */
export type PageOption = {
  name: string
  /** Empty or absent when the page names the option but lists no values. */
  values?: string[]
  /** One of `values`: what the page or the link picks. */
  selected?: string | null
}

/**
 * The value of the dropdown's first choice, labeled "Not sure". It writes nothing
 * to the notes. It is empty, so a page value that is literally "Not sure" stays a
 * real choice. The extractor never gives an empty value.
 */
export const NOT_SURE = ''

/** The label of the dropdown's first choice. */
export const NOT_SURE_LABEL = 'Not sure'

/** The options that get a dropdown: the page lists their values. */
export const pickable = (options: PageOption[]) => options.filter((o) => (o.values?.length ?? 0) > 0)

/** The option names with no values. Notes starts with a line for each. */
export const headerLines = (options: PageOption[]) => options.filter((o) => (o.values?.length ?? 0) === 0).map((o) => `${o.name}:`)

/** Where each dropdown starts: the value the page picks, otherwise Not sure. */
export const chosenFor = (options: PageOption[]): Record<string, string> =>
  Object.fromEntries(pickable(options).map((o) => [o.name, o.selected ?? NOT_SURE]))

/** The Notes box when the form opens: `prefill`, or a header line for each option with no values. */
export const startingNotes = (prefill: string, options: PageOption[]) => prefill || headerLines(options).join('\n')

/**
 * The notes to save. Each dropdown choice goes first as a "Name: value" line.
 * Not sure writes nothing. A header line the member left unchanged is dropped.
 * Everything else the member typed stays.
 */
export function notesToSave(typed: string, options: PageOption[], chosen: Record<string, string>): string {
  const headers = new Set(headerLines(options))
  const kept = typed.split('\n').filter((line) => !headers.has(line.trim()))
  const rest = kept.join('\n').trim()
  const typedLines = new Set(rest.split('\n').map((l) => l.trim()))
  const lines = pickable(options)
    .map((o) => ({ name: o.name, value: chosen[o.name] ?? NOT_SURE }))
    .filter((c) => c.value !== NOT_SURE)
    .map((c) => `${c.name}: ${c.value}`)
    .filter((line) => !typedLines.has(line))
  return [...lines, rest].filter(Boolean).join('\n')
}
