import { useId, useState, type FormEvent, type ReactNode } from 'react'
import type { OccasionInput, Rule, RuleInput, RuleKind } from './api'
import { months } from './BirthdayFields'
import { notice } from './Picker'
import { field, primary, select } from './ui'

export const weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const nths = ['1st', '2nd', '3rd', '4th']

/** The four date rules, each with a name and an example. */
const kinds: { kind: RuleKind; name: string; example: string }[] = [
  { kind: 'annual', name: 'Same date every year', example: 'Like Dec 25' },
  { kind: 'once', name: 'One time only', example: 'Like May 14, 2027' },
  { kind: 'nth_weekday', name: 'A set weekday every year', example: 'Like the 2nd Sunday of May' },
  { kind: 'last_weekday', name: 'Last weekday of a month', example: 'Like the last Monday of May' },
]

/** A rule choice: `ink` on `tile` when off, the two swapped when on. */
const choice = (on: boolean) =>
  `flex min-h-16 w-full flex-col items-start justify-center rounded-md px-4 py-2 text-left ${on ? 'bg-ink text-bg' : 'bg-tile text-ink'}`

/** The rule in words, such as "Every year on May 6" or "2nd Sunday of May, every year". */
export function describeRule(r: Rule): string {
  const month = months[r.month - 1]
  switch (r.kind) {
    case 'annual':
      return `Every year on ${month} ${r.day}`
    case 'once':
      return 'One time only'
    case 'nth_weekday':
      return `${nths[(r.n ?? 1) - 1]} ${weekdays[r.weekday ?? 0]} of ${month}, every year`
    case 'last_weekday':
      return `Last ${weekdays[r.weekday ?? 0]} of ${month}, every year`
  }
}

const two = (n: number) => String(n).padStart(2, '0')

/** The date input's text for a one-time rule, or empty when it has no date yet. */
function dateText(rule?: Rule) {
  return rule?.kind === 'once' && rule.year && rule.day ? `${rule.year}-${two(rule.month)}-${two(rule.day)}` : ''
}

/**
 * A name and one of the four date rules, for a custom occasion. The server checks
 * that the date exists. `onSubmit` returns the words of a refusal to show, or
 * null when the change was saved.
 */
export function OccasionForm({
  label,
  initial,
  onSubmit,
  extra,
}: {
  /** The submit button's text. */
  label: string
  initial?: { name: string; rule: Rule }
  onSubmit: (input: OccasionInput) => Promise<string | null>
  /** More to ask, under the date and above the button, such as a shared occasion's Everyone switch. */
  extra?: ReactNode
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [kind, setKind] = useState<RuleKind>(initial?.rule.kind ?? 'annual')
  const [month, setMonth] = useState(String(initial?.rule.month ?? ''))
  const [day, setDay] = useState(initial?.rule.kind === 'annual' && initial.rule.day ? String(initial.rule.day) : '')
  const [date, setDate] = useState(dateText(initial?.rule))
  const [weekday, setWeekday] = useState(String(initial?.rule.weekday ?? 0))
  const [nth, setNth] = useState(String(initial?.rule.n ?? 1))
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (!name.trim()) return setError('Enter a name for the occasion.')
    let rule: RuleInput
    if (kind === 'once') {
      const [y, m, d] = date.split('-').map(Number)
      if (!y || !m || !d) return setError('Choose the date.')
      rule = { kind, year: y, month: m, day: d }
    } else {
      if (!month) return setError('Choose a month.')
      rule = { kind, month: Number(month) }
      if (kind === 'annual') {
        if (!day) return setError('Enter the day of the month.')
        rule.day = Number(day)
      } else {
        rule.weekday = Number(weekday)
        if (kind === 'nth_weekday') rule.n = Number(nth)
      }
    }
    setBusy(true)
    try {
      const err = await onSubmit({ name: name.trim(), rule })
      if (err) setError(err)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  const monthSelect = (id: string) => (
    <select id={id} className={select} value={month} onChange={(e) => setMonth(e.target.value)}>
      <option value="">Choose a month</option>
      {months.map((m, i) => (
        <option key={m} value={i + 1}>
          {m}
        </option>
      ))}
    </select>
  )
  const weekdaySelect = (id: string) => (
    <select id={id} className={select} value={weekday} onChange={(e) => setWeekday(e.target.value)}>
      {weekdays.map((w, i) => (
        <option key={w} value={i}>
          {w}
        </option>
      ))}
    </select>
  )

  return (
    <form onSubmit={submit} className="flex max-w-xl flex-col gap-5" noValidate>
      <Field label="What's it called?" bold>
        {(id) => <input id={id} className={field} value={name} onChange={(e) => setName(e.target.value)} />}
      </Field>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 font-semibold">When is it?</legend>
        {kinds.map((k) => (
          <button key={k.kind} type="button" aria-pressed={kind === k.kind} className={choice(kind === k.kind)} onClick={() => setKind(k.kind)}>
            <strong className="font-extrabold">{k.name}</strong>
            <span>{k.example}</span>
          </button>
        ))}
      </fieldset>
      {kind === 'annual' && (
        <div className="flex gap-3">
          <div className="flex-1">
            <Field label="Month">{monthSelect}</Field>
          </div>
          <div className="w-28">
            <Field label="Day">
              {(id) => (
                <input id={id} className={field} type="number" inputMode="numeric" min={1} max={31} value={day} onChange={(e) => setDay(e.target.value)} />
              )}
            </Field>
          </div>
        </div>
      )}
      {kind === 'once' && (
        <Field label="Date">
          {(id) => <input id={id} className={field} type="date" value={date} onChange={(e) => setDate(e.target.value)} />}
        </Field>
      )}
      {kind === 'nth_weekday' && (
        <div className="flex flex-wrap gap-3">
          <div className="w-24">
            <Field label="Which">
              {(id) => (
                <select id={id} className={select} value={nth} onChange={(e) => setNth(e.target.value)}>
                  {nths.map((n, i) => (
                    <option key={n} value={i + 1}>
                      {n}
                    </option>
                  ))}
                </select>
              )}
            </Field>
          </div>
          <div className="min-w-40 flex-1">
            <Field label="Day">{weekdaySelect}</Field>
          </div>
          <div className="min-w-40 flex-1">
            <Field label="Month">{monthSelect}</Field>
          </div>
        </div>
      )}
      {kind === 'last_weekday' && (
        <div className="flex flex-wrap gap-3">
          <div className="min-w-40 flex-1">
            <Field label="Day">{weekdaySelect}</Field>
          </div>
          <div className="min-w-40 flex-1">
            <Field label="Month">{monthSelect}</Field>
          </div>
        </div>
      )}
      {extra}
      {error && (
        <p role="alert" className={notice}>
          {error}
        </p>
      )}
      <button type="submit" className={primary} disabled={busy}>
        {label}
      </button>
    </form>
  )
}

/** A label above a control. The control gets the id, so the label's text is only its own. */
function Field({ label, bold = false, children }: { label: string; bold?: boolean; children: (id: string) => ReactNode }) {
  const id = useId()
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className={bold ? 'font-semibold' : undefined}>
        {label}
      </label>
      {children(id)}
    </div>
  )
}
