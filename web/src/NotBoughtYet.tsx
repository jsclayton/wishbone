import type { Gift } from './api'
import { joinNames } from './gifts'
import { useCollection } from './useCollection'

/** "Socks (2) for Mom". */
function describe(g: Gift): string {
  const who = g.recipients && g.recipients.length > 0 ? joinNames(g.recipients.map((r) => r.name)) : g.list.name
  return `${g.title}${g.quantity > 1 ? ` (${g.quantity})` : ''} for ${who}`
}

/** What happened to the gift, because "Not bought yet" doesn't describe a changed or removed item. */
function flagLine(g: Gift): string | null {
  const codes = (g.flags ?? []).map((f) => f.code)
  if (codes.includes('removed')) return 'Removed from the list'
  if (codes.includes('changed')) return 'Changed since you picked it'
  return null
}

/**
 * Home's "Not bought yet": the giver's open gifts that need attention, which
 * the server works out (not bought and given within 14 days, or flagged
 * changed or removed). It is hidden when there are none, and when the
 * gifts can't load, so a failure here doesn't hide the rest of Home.
 */
export function NotBoughtYet() {
  const { loaded } = useCollection<Gift>('/api/v2/gifts?giver_id=me&state=open&needs_attention=true')
  const gifts = loaded.status === 'ready' ? loaded.rows : []
  if (gifts.length === 0) return null
  return (
    <section
      aria-labelledby="not-bought-heading"
      className="flex flex-col gap-0.5 rounded-md bg-alert px-3.5 py-3 text-on-alert md:flex-row md:flex-wrap md:items-center md:gap-x-6"
    >
      <h2 id="not-bought-heading" className="font-extrabold">
        Not bought yet
      </h2>
      <ul className="flex min-w-0 flex-col md:flex-1">
        {gifts.map((g) => {
          const line = flagLine(g)
          return (
            <li key={g.id}>
              <a href={`/giving#gift-${g.id}`} className="flex min-h-11 flex-col justify-center font-semibold">
                <span className="underline">{describe(g)}</span>
                {line && <span className="font-normal">{line}</span>}
              </a>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
