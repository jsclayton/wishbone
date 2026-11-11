import type { ReactNode } from 'react'
import type { Occasion, User } from './api'
import { UserAvatar } from './Avatar'
import { useCollection, type Loaded } from './useCollection'

const weekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const shortMonths = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/** How far ahead Home looks: today plus 60 days (docs/routes.md, Occasion routes). */
const WINDOW_DAYS = 60

/** An ISO date at noon UTC, so day arithmetic stays clear of daylight saving. */
function asUTC(iso: string): Date {
  const [y = 1970, m = 1, d = 1] = iso.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, d, 12))
}

/** "2026-11-08" for a date in the browser's zone. */
function isoDate(d: Date): string {
  const two = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())}`
}

/** "Nov 14" */
export function shortDate(iso: string): string {
  const d = asUTC(iso)
  return `${shortMonths[d.getUTCMonth()]} ${d.getUTCDate()}`
}

/** "Sat Nov 14" */
export function longDate(iso: string): string {
  return `${weekdays[asUTC(iso).getUTCDay()]} ${shortDate(iso)}`
}

/**
 * Days from today in the browser's zone. The server works out a next date where
 * the occasion's user lives (a shared occasion: where the caller lives), so a
 * date can be a day behind the browser's: that is still the day, not a negative
 * count.
 */
export function daysUntil(iso: string): number {
  const now = new Date()
  const today = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate(), 12)
  return Math.max(0, Math.round((asUTC(iso).getTime() - today) / 86_400_000))
}

/** "1 day", "27 days", or "Today". */
export function countdown(days: number): string {
  return days === 0 ? 'Today' : days === 1 ? '1 day' : `${days} days`
}

/** The occasions in the next 60 days that the user can see, soonest first. */
export function upcomingPath(): string {
  const end = new Date()
  end.setDate(end.getDate() + WINDOW_DAYS)
  return `/api/v2/occasions?next_date_before=${isoDate(end)}`
}

/** What Home's band and Coming up share: the occasions in the window, loaded once. */
export function useUpcoming(): Loaded<Occasion> {
  return useCollection<Occasion>(upcomingPath()).loaded
}

/** An occasion that has a next date. The window only holds those. */
export type Upcoming = Occasion & { next_date: string }

const hasDate = (o: Occasion): o is Upcoming => o.next_date !== null

/** The occasions the band speaks of: those on the first day that has one, never the user's own birthday or custom occasion. Empty when there are none. */
export function nextOccasions(occasions: Occasion[], me: User): Upcoming[] {
  const dated = occasions.filter(hasDate)
  const first = dated.find((o) => o.kind === 'shared' || o.user?.id !== me.id)
  if (!first) return []
  return dated.filter((o) => o.next_date === first.next_date && (o.kind === 'shared' || o.user?.id !== me.id))
}

/** Where an occasion opens: a shared occasion opens who is on it, any other opens its user's page. */
export function occasionPath(o: Occasion): string {
  return o.kind === 'shared' || !o.user ? `/occasions/${o.id}` : `/users/${o.user.id}`
}

/** "Theo's birthday", "Your Anniversary", or a shared occasion's own name. */
export function occasionName(o: Occasion, me: User): string {
  if (o.kind === 'shared' || !o.user) return o.name
  const whose = o.user.id === me.id ? 'Your' : `${o.user.name}'s`
  return `${whose} ${o.kind === 'birthday' ? 'birthday' : o.name}`
}

/** Lines up like a table. A long name takes its own line rather than split a word. */
const row = 'flex min-h-15 flex-wrap items-center gap-x-2 py-1'

function Row({ occasion: o, me }: { occasion: Upcoming; me: User }) {
  const days = daysUntil(o.next_date)
  return (
    <a href={occasionPath(o)} className={row}>
      <time dateTime={o.next_date} className="w-19">
        {shortDate(o.next_date)}
      </time>
      {o.user ? <UserAvatar user={o.user} size={32} /> : <span className="size-8" />}
      <span className="max-w-full flex-1 font-semibold wrap-break-word">{occasionName(o, me)}</span>
      <span className="ml-auto font-mono text-mute">{days === 0 ? 'Today' : `${days}d`}</span>
    </a>
  )
}

/** Home's "Coming up": the occasions in the next 60 days. The band speaks for an empty list. */
export function ComingUp({ loaded, me }: { loaded: Loaded<Occasion>; me: User }): ReactNode {
  if (loaded.status === 'failed') return <p role="alert">Could not load upcoming occasions. Reload and try again.</p>
  if (loaded.status !== 'ready') return null
  const rows = loaded.rows.filter(hasDate)
  if (rows.length === 0) return null
  return (
    <section aria-labelledby="upcoming-heading" className="flex min-w-0 flex-col">
      <h2 id="upcoming-heading" className="mb-1 text-[1.375rem]/normal font-extrabold">
        Coming up
      </h2>
      <ul>
        {rows.map((o) => (
          <li key={o.id} className="border-t border-hair last:border-b">
            <Row occasion={o} me={me} />
          </li>
        ))}
      </ul>
    </section>
  )
}
