import type { ReactNode } from 'react'
import type { Gift, ReceivedGift, User } from './api'
import { UserAvatar } from './Avatar'
import { GiftPicture } from './Giving'
import { Screen } from './Screen'
import { useCollection } from './useCollection'
import { primary } from './ui'

/** "Dec 25, 2026". */
export function dateWithYear(iso: string): string {
  const [y = 1970, m = 1, d = 1] = iso.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, d)).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })
}

/** A given gift, from either side: what a recipient got or what a giver gave. */
type Given = Pick<Gift, 'id' | 'title' | 'quantity' | 'image' | 'occasion_name' | 'occasion_date' | 'recipients' | 'list'>

type Group<T extends Given> = { key: string; name: string; date: string; gifts: T[] }

/** Groups gifts by occasion and date, keeping the order given (newest first). */
export function groupByOccasion<T extends Given>(gifts: T[], nameOf: (gift: T) => string = (g) => g.occasion_name ?? 'Occasion'): Group<T>[] {
  const groups: Group<T>[] = []
  for (const g of gifts) {
    const name = nameOf(g)
    const key = `${name}|${g.occasion_date}`
    let group = groups.find((x) => x.key === key)
    if (!group) {
      group = { key, name, date: g.occasion_date, gifts: [] }
      groups.push(group)
    }
    group.gifts.push(g)
  }
  return groups
}

/** The header of a group is a band that runs to the screen's edges on a phone. */
export function GroupSection<T extends Given>({ group, children }: { group: Group<T>; children: ReactNode }) {
  return (
    <section aria-label={`${group.name}, ${dateWithYear(group.date)}`} className="-mx-4 flex flex-col sm:mx-0 sm:gap-3">
      <h2 className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 bg-tile px-4 py-2.5 text-lg font-extrabold sm:rounded-md">
        <span className="min-w-0 wrap-break-word">{group.name}</span>
        <time dateTime={group.date} className="ml-auto shrink-0 text-[1.0625rem] font-semibold">
          {dateWithYear(group.date)}
        </time>
      </h2>
      <ul className="flex flex-col">{children}</ul>
    </section>
  )
}

/** "Socks (2)": the title, with the quantity when it is more than one. */
export function titleWithQuantity(g: Pick<Given, 'title' | 'quantity'>): string {
  return `${g.title}${g.quantity > 1 ? ` (${g.quantity})` : ''}`
}

/**
 * Received: the gifts given to the signed-in user, newest first, with who gave
 * them (docs/spec.md, Gifts, Screens). A gift with a from label reads "From
 * Emma · claimed by Mom". Without one it reads "From Mom". The recipient never
 * sees the giver's note or whether they bought it: the server leaves them out.
 */
export function Received({ me }: { me: User }) {
  const { loaded } = useCollection<ReceivedGift>('/api/v2/gifts?recipient_id=me&sort=-given_at')

  if (loaded.status === 'failed') {
    return (
      <Screen title="Received">
        <p role="alert">Could not load what you received. Reload and try again.</p>
      </Screen>
    )
  }
  if (loaded.status === 'loading') return null

  return (
    <Screen title="Received">
      {loaded.rows.length === 0 && (
        <div className="flex max-w-xl flex-col gap-3">
          <h2 className="text-[1.75rem]/[1.2] font-extrabold">Nothing here yet</h2>
          <p className="text-xl">After an occasion, the gifts you got show up here, with who gave them.</p>
          <a href="/lists" className={`${primary} flex items-center justify-center`}>
            Go to My lists
          </a>
        </div>
      )}
      {groupByOccasion(loaded.rows, (g) => occasionTitle(g, me)).map((group) => (
        <GroupSection key={group.key} group={group}>
          {group.gifts.map((g) => (
            <ReceivedRow key={g.id} gift={g} />
          ))}
        </GroupSection>
      ))}
    </Screen>
  )
}

/** The name of an occasion in this history: the user's own birthday is "Your birthday". */
function occasionTitle(g: ReceivedGift, me: User): string {
  const name = g.occasion_name ?? 'Occasion'
  return name === 'Birthday' && g.recipients?.length === 1 && g.recipients[0]?.id === me.id ? 'Your birthday' : name
}

/** "From Emma · claimed by Mom", or "From Mom" when the gift has no from label. */
export function fromText(g: Pick<Gift, 'from_label' | 'giver_name'>): string {
  return g.from_label ? `From ${g.from_label} · claimed by ${g.giver_name}` : `From ${g.giver_name}`
}

/** A link that is the whole of its line, in the weight of the top row's links. */
const quiet = 'flex min-h-12 items-center font-semibold whitespace-nowrap underline'

function ReceivedRow({ gift: g }: { gift: ReceivedGift }) {
  return (
    <li className="flex flex-col gap-3 border-b border-hair px-4 py-4 last:border-b-0 sm:px-0">
      <div className="flex items-center gap-4">
        <GiftPicture gift={g} />
        <div className="flex min-w-0 flex-col gap-1.5">
          <h3 className="text-xl font-extrabold wrap-break-word">{titleWithQuantity(g)}</h3>
          <p className="flex items-center gap-2">
            <UserAvatar user={g.giver ?? { name: g.giver_name, avatar: null }} size={24} />
            <span className="min-w-0 wrap-break-word">{fromText(g)}</span>
          </p>
        </div>
      </div>
      {(g.can_add_again || g.can_restore) && (
        <div className="flex flex-wrap items-center gap-x-6">
          {g.can_add_again && (
            <a href={`/gifts/${g.id}/add-again`} className={quiet} aria-label={`Add again: ${g.title}`}>
              Add again
            </a>
          )}
          {g.can_restore && (
            <a href={`/gifts/${g.id}/restore`} className={quiet} aria-label={`I didn't get this: ${g.title}`}>
              {"I didn't get this"}
            </a>
          )}
        </div>
      )}
    </li>
  )
}
