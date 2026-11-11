import type { Occasion, User } from './api'
import { occasionName, shortDate } from './Upcoming'

/** "Dec 9", with the year when it isn't this year. */
export function dayLabel(iso: string): string {
  const year = Number(iso.slice(0, 4))
  return year === new Date().getFullYear() ? shortDate(iso) : `${shortDate(iso)}, ${year}`
}

/** An occasion that has a next date: the only kind a gift can be for. */
export type Pickable = Occasion & { next_date: string }

/** The occasions of `GET /occasions?list_id=` that have a date, in the order the server gave. */
export function pickable(occasions: Occasion[]): Pickable[] {
  return occasions.filter((o): o is Pickable => o.next_date !== null)
}

/**
 * "Which occasion?": one row for each, the chosen one inverted. The rows are
 * radio buttons, so the keyboard and screen readers get a group. A birthday is
 * named with its user ("Mom's birthday"), because several recipients can be on
 * one list.
 */
export function OccasionPicker({ occasions, value, me, onChange }: { occasions: Pickable[]; value: string; me: User; onChange: (id: string) => void }) {
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="pb-2 text-[1.375rem] font-extrabold">Which occasion?</legend>
      {occasions.map((o) => (
        <label
          key={o.id}
          className={`flex min-h-14 cursor-pointer items-center justify-between gap-3 rounded-md px-4 py-3 text-lg has-focus-visible:outline-2 has-focus-visible:outline-offset-2 has-focus-visible:outline-ink ${
            value === o.id ? 'bg-ink text-bg' : 'bg-tile text-ink'
          }`}
        >
          <input type="radio" name="occasion" className="sr-only" value={o.id} checked={value === o.id} onChange={() => onChange(o.id)} />
          <strong className="font-extrabold">{occasionName(o, me)}</strong>
          <time dateTime={o.next_date} className="font-mono">
            {dayLabel(o.next_date)}
          </time>
        </label>
      ))}
    </fieldset>
  )
}
