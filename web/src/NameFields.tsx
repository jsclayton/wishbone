import { field } from './ui'

/**
 * The first and last name inputs, shared by every form that edits a name. The
 * first name is required. The weight sits on a span, because the inputs
 * inherit the label's font.
 */
export function NameFields({
  first,
  last,
  onFirst,
  onLast,
  own = false,
}: {
  first: string
  last: string
  onFirst: (v: string) => void
  onLast: (v: string) => void
  /** On a user's own form, the inputs carry the browser's name autofill hints. */
  own?: boolean
}) {
  return (
    <>
      <label className="flex flex-col gap-1.5">
        <span className="font-semibold">First name</span>
        <input className={field} value={first} onChange={(e) => onFirst(e.target.value)} autoComplete={own ? 'given-name' : undefined} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="font-semibold">Last name (optional)</span>
        <input className={field} value={last} onChange={(e) => onLast(e.target.value)} autoComplete={own ? 'family-name' : undefined} />
      </label>
    </>
  )
}
