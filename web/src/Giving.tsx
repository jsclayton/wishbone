import { useEffect, useState, type ReactNode } from 'react'
import { acknowledgeGift, deleteGift, fetchAll, updateGift, type Gift, type GiftChanges, type GiftFlag, type Occasion, type User } from './api'
import { Confirm } from './Confirm'
import { ChangeList } from './GiftChanges'
import { birthdayOf, joinNames, occasionText, revealDay } from './gifts'
import { useBirthdayOwners } from './useBirthdayOwners'
import { HowMany } from './HowMany'
import { dayLabel, pickable, type Pickable } from './OccasionPicker'
import { replaceTo } from './navigation'
import { notice } from './Picker'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { daysUntil, occasionName, shortDate } from './Upcoming'
import { useCollection } from './useCollection'
import { button, field, primary, select } from './ui'

/** The gifts of one list for one occasion, as Giving draws them under one header. */
type Group = { key: string; who: string; label: string; date: string | null; gifts: Gift[]; attention: boolean }

/** "Mom, First Advent": who the gifts are for, and the occasion or, when it is hidden, the day they are given. */
function describe(gift: Gift, owners: Map<string, string>): { who: string; label: string } {
  const who = birthdayOf(gift, owners) ?? (gift.recipients && gift.recipients.length > 0 ? joinNames(gift.recipients.map((r) => r.name)) : gift.list.name)
  const o = gift.occasion
  const label = o ? (o.kind === 'birthday' ? 'birthday' : o.name) : `hidden until ${revealDay(gift)}`
  return { who, label }
}

/**
 * Groups gifts by list, occasion and date, in the server's order (soonest
 * reveal first). A group with a gift that needs attention (not bought and given
 * within 14 days, or flagged changed or removed) goes to the top; both parts
 * keep their order.
 */
export function groupGifts(gifts: Gift[], owners: Map<string, string> = new Map()): Group[] {
  const groups: Group[] = []
  for (const g of gifts) {
    const key = `${g.list.id}:${g.occasion?.id ?? 'hidden'}:${g.occasion_date}`
    let group = groups.find((x) => x.key === key)
    if (!group) {
      group = { key, ...describe(g, owners), date: g.occasion ? g.occasion_date : null, gifts: [], attention: false }
      groups.push(group)
    }
    group.gifts.push(g)
    group.attention ||= g.needs_attention ?? false
  }
  return [...groups.filter((g) => g.attention), ...groups.filter((g) => !g.attention)]
}

/**
 * Giving: the giver's own open gifts, with bought, a private note, who the gift
 * is from, how many, and the occasion (docs/spec.md, Gifts, Screens). Only
 * the giver can see them.
 */
export function Giving({ me }: { me: User }) {
  const { loaded, reload } = useCollection<Gift>('/api/v2/gifts?giver_id=me&state=open')
  const ready = loaded.status === 'ready'
  const owners = useBirthdayOwners(ready ? loaded.rows : [])
  useEffect(() => {
    // The gifts load after the page, so the browser can't jump to #gift-N itself.
    if (ready && window.location.hash) document.getElementById(window.location.hash.slice(1))?.scrollIntoView?.({ block: 'center' })
  }, [ready])

  if (loaded.status === 'failed') {
    return (
      <Screen title="Giving">
        <p role="alert">Could not load your gifts. Reload and try again.</p>
      </Screen>
    )
  }
  if (loaded.status === 'loading') return null

  return (
    <Screen title="Giving">
      {loaded.rows.length === 0 && (
        <div className="flex max-w-xl flex-col gap-3">
          <h2 className="text-[1.75rem]/[1.2] font-extrabold">Nothing yet</h2>
          <p className="text-xl">{"Tap I'll get this on an item and it shows up here. Only you can see it."}</p>
          <a href="/" className={`${primary} flex items-center justify-center`}>
            {"See people's lists"}
          </a>
        </div>
      )}
      {groupGifts(loaded.rows, owners).map((g) => (
        <GroupSection key={g.key} group={g} me={me} onChange={reload} />
      ))}
    </Screen>
  )
}

/** The header of a group is a band that runs to the screen's edges on a phone. */
function GroupSection({ group: g, me, onChange }: { group: Group; me: User; onChange: () => void }) {
  const title = `${g.who}, ${g.label}`
  return (
    <section aria-label={title} className="-mx-4 flex flex-col sm:mx-0 sm:gap-3">
      <h2 className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 bg-tile px-4 py-2.5 text-lg font-extrabold sm:rounded-md">
        <span>{title}</span>
        {g.date && (
          <time dateTime={g.date} className="ml-auto shrink-0 text-[1.0625rem] font-semibold">
            {shortDate(g.date)}, {daysUntil(g.date)}d
          </time>
        )}
      </h2>
      <ul className="flex flex-col">
        {g.gifts.map((gift) => (
          <GiftRow key={gift.id} gift={gift} me={me} onChange={onChange} />
        ))}
      </ul>
    </section>
  )
}

/** The item's picture, or a plain box when it has none or can't be read any more. */
export function GiftPicture({ gift }: { gift: Pick<Gift, 'image'> }) {
  const [broken, setBroken] = useState(false)
  return (
    <div className="flex size-20 items-center justify-center rounded-md bg-tile">
      {gift.image && !broken ? (
        <img src={gift.image.url} alt="" loading="lazy" onError={() => setBroken(true)} className="size-full rounded-md object-contain" />
      ) : (
        <svg width="40" height="40" viewBox="0 0 96 96" fill="none" stroke="currentColor" strokeWidth="4" aria-hidden="true">
          <rect x="12" y="32" width="72" height="48" />
          <path d="M12 48 H84 M48 32 V80" />
        </svg>
      )}
    </div>
  )
}

const toggle = 'min-h-14 rounded-md px-3 py-3 text-lg font-extrabold whitespace-nowrap sm:px-6'
const on = 'bg-ink text-bg'
const off = 'bg-tile text-ink'
/** A button that is the whole of its line, in the weight of the top row's links. */
const quiet = 'flex min-h-12 items-center font-semibold whitespace-nowrap underline'

/** Which part of a gift is being edited. */
type Editing = 'note' | 'from' | 'occasion' | null

function GiftRow({ gift: g, me, onChange }: { gift: Gift; me: User; onChange: () => void }) {
  const [editing, setEditing] = useState<Editing>(null)
  const [text, setText] = useState('')
  const [choices, setChoices] = useState<Pickable[] | undefined>(undefined)
  const [occasion, setOccasion] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  /** Saves a change. One runs at a time. The list loads again after it, because a change of occasion can merge two gifts. */
  async function patch(changes: GiftChanges): Promise<boolean> {
    if (busy) return false
    setBusy(true)
    setError(null)
    try {
      const res = await updateGift(g.id, changes)
      if (!res.ok) {
        setError(res.detail)
        return false
      }
      onChange()
      return true
    } catch {
      setError('Could not save. Try again.')
      return false
    } finally {
      setBusy(false)
    }
  }

  /** Answers flags: clears them, and loads the gifts again. One runs at a time. */
  async function answer(codes: GiftFlag['code'][]) {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const res = await acknowledgeGift(g.id, codes)
      if (!res.ok) {
        setError(res.detail)
        return
      }
      onChange()
    } catch {
      setError('Could not save. Try again.')
    } finally {
      setBusy(false)
    }
  }

  function begin(part: 'note' | 'from') {
    setText((part === 'note' ? g.note : g.from_label) ?? '')
    setEditing(part)
    setError(null)
  }

  async function beginOccasion() {
    setEditing('occasion')
    setError(null)
    try {
      const rows = pickable(await fetchAll<Occasion>(`/api/v2/occasions?list_id=${g.list.id}`))
      if (rows.length === 0) {
        // The giver sees none of the other recipients: the server picked the occasion.
        setEditing(null)
        setError('You can see none of the other recipients, so the occasion is picked for you.')
        return
      }
      setChoices(rows)
      setOccasion((rows.find((o) => o.id === g.occasion?.id) ?? rows[0])?.id ?? '')
    } catch {
      setError('Could not load the occasions. Try again.')
    }
  }

  const label = g.title
  const count = g.can_raise_quantity || g.quantity > 1

  return (
    <li id={`gift-${g.id}`} className="flex scroll-mt-4 flex-col gap-2.5 px-4 pt-3.5 pb-4.5 sm:flex-row sm:flex-wrap sm:items-center sm:gap-x-6 sm:py-3">
      <div className="grid min-w-0 grid-cols-[5rem_minmax(0,1fr)] items-center gap-3 sm:flex-[3_1_21rem]">
        <GiftPicture gift={g} />
        <div className="flex flex-col">
          <h3 className="text-xl/[1.2] font-extrabold">{g.quantity > 1 ? `${g.title} (${g.quantity})` : g.title}</h3>
          <p className="text-mute">
            Hidden until <time dateTime={g.reveal_at}>{revealDay(g)}</time>
          </p>
          {g.from_label && <p>From {g.from_label}</p>}
          {g.note && editing !== 'note' && <p>{g.note}</p>}
        </div>
      </div>
      {(g.flags ?? []).length > 0 && (
        <div className="flex flex-col gap-2.5 sm:order-last sm:basis-full">
          {(g.flags ?? []).map((flag) => (
            <FlagStrip key={flag.code} flag={flag} gift={g} disabled={busy} onAnswer={(codes) => void answer(codes)} />
          ))}
        </div>
      )}
      {count && (
        <fieldset aria-label={`How many: ${label}`} className="flex min-w-0 flex-col gap-2 sm:max-w-xs sm:flex-[1_1_100%]">
          <h4 className="font-extrabold">How many</h4>
          <HowMany value={g.quantity} most={g.can_raise_quantity ? undefined : g.quantity} label={label} onChange={(quantity) => void patch({ quantity })} />
        </fieldset>
      )}
      <fieldset aria-label={`Bought: ${label}`} className="grid min-w-0 grid-cols-2 gap-2 sm:flex-[2_1_17.5rem]">
        <button
          type="button"
          aria-pressed={!g.is_bought}
          aria-label={`Not bought: ${label}`}
          className={`${toggle} ${g.is_bought ? off : on}`}
          onClick={() => g.is_bought && void patch({ is_bought: false })}
        >
          Not bought
        </button>
        <button
          type="button"
          aria-pressed={g.is_bought}
          aria-label={`Bought: ${label}`}
          className={`${toggle} ${g.is_bought ? on : off}`}
          onClick={() => !g.is_bought && void patch({ is_bought: true })}
        >
          Bought
        </button>
      </fieldset>
      <div className="flex flex-wrap items-center gap-x-6 sm:flex-[0_1_auto]">
        <button type="button" className={quiet} aria-label={`${g.note ? 'Edit note' : 'Add a note'}: ${label}`} onClick={() => begin('note')}>
          {g.note ? 'Edit note' : 'Add a note'}
        </button>
        <button
          type="button"
          className={quiet}
          aria-label={`${g.from_label ? 'Edit who it is from' : 'Add who it is from'}: ${label}`}
          onClick={() => begin('from')}
        >
          {g.from_label ? 'Edit who it is from' : 'Add who it is from'}
        </button>
        {editing !== 'occasion' && (
          <button type="button" className={quiet} aria-label={`Change occasion: ${label}`} onClick={() => void beginOccasion()}>
            Change occasion
          </button>
        )}
        <a href={`/gifts/${g.id}/unclaim`} className={quiet} aria-label={`I won't get this: ${label}`}>
          {"I won't get this"}
        </a>
      </div>
      {editing === 'note' && (
        <div className="flex flex-col gap-2 sm:order-last sm:basis-full">
          <label className="flex flex-col gap-1">
            Private note
            <textarea className={field} rows={2} maxLength={2000} value={text} aria-label={`Private note: ${label}`} onChange={(e) => setText(e.target.value)} />
          </label>
          <Save
            label={`Save note: ${label}`}
            busy={busy}
            unchanged={text.trim() === (g.note ?? '')}
            onSave={async () => (await patch({ note: text.trim() === '' ? null : text.trim() })) && setEditing(null)}
            onCancel={() => setEditing(null)}
          />
        </div>
      )}
      {editing === 'from' && (
        <div className="flex flex-col gap-2 sm:order-last sm:basis-full">
          <label className="flex flex-col gap-1">
            {"Who it's from, if that isn't only you"}
            <input className={field} maxLength={60} value={text} aria-label={`From: ${label}`} onChange={(e) => setText(e.target.value)} />
          </label>
          <Save
            label={`Save from: ${label}`}
            busy={busy}
            unchanged={text.trim() === (g.from_label ?? '')}
            onSave={async () => (await patch({ from_label: text.trim() === '' ? null : text.trim() })) && setEditing(null)}
            onCancel={() => setEditing(null)}
          />
        </div>
      )}
      {editing === 'occasion' && (
        <div className="flex flex-col gap-2 sm:order-last sm:basis-full">
          <label className="flex flex-col gap-1">
            Occasion
            <select className={select} value={occasion} onChange={(e) => setOccasion(e.target.value)} disabled={!choices}>
              {(choices ?? []).map((o) => (
                <option key={o.id} value={o.id}>
                  {occasionName(o, me)}, {dayLabel(o.next_date)}
                </option>
              ))}
            </select>
          </label>
          <Save
            label={`Save occasion: ${label}`}
            busy={busy}
            unchanged={!occasion || occasion === g.occasion?.id}
            onSave={async () => (await patch({ occasion_id: occasion })) && setEditing(null)}
            onCancel={() => setEditing(null)}
          />
        </div>
      )}
      {error && (
        <div className="sm:order-last sm:basis-full">
          <p role="alert" className={notice}>
            {error}
          </p>
        </div>
      )}
    </li>
  )
}

/** A flag on a gift: a short bold statement and one sentence on a citron strip, then buttons that answer it. */
function Strip({ lead, text, actions, children }: { lead: string; text?: string; actions: ReactNode[]; children?: ReactNode }) {
  return (
    <>
      <div className="rounded-md bg-alert px-3 py-2.5 text-on-alert">
        <strong className="font-extrabold">{lead}</strong> {text}
        {children}
      </div>
      <div className={`grid gap-2 sm:max-w-md ${actions.length > 1 ? 'grid-cols-2' : ''}`}>{actions}</div>
    </>
  )
}

/** One flag of a gift, with the buttons that answer it. A flag this screen has no words for shows nothing. */
function FlagStrip({ flag, gift, disabled, onAnswer }: { flag: GiftFlag; gift: Gift; disabled: boolean; onAnswer: (codes: GiftFlag['code'][]) => void }) {
  const got = (code: GiftFlag['code'], name: string, label = 'Got it') => (
    <button key={code} type="button" className={button} disabled={disabled} aria-label={`${name}: ${gift.title}`} onClick={() => onAnswer([code])}>
      {label}
    </button>
  )
  switch (flag.code) {
    case 'changed':
      return (
        <Strip lead="Changed since you picked it." actions={[got('changed', 'Got it, changes')]}>
          <ChangeList changes={flag.fields} />
        </Strip>
      )
    case 'removed':
      return (
        <Strip
          lead="Removed from the list."
          text="You can still give it, or drop it."
          actions={[
            got('removed', 'Keep it', 'Keep it'),
            <a key="drop" href={`/gifts/${gift.id}/unclaim`} className={`${button} flex items-center justify-center`} aria-label={`Drop it: ${gift.title}`}>
              Drop it
            </a>,
          ]}
        />
      )
    case 'moved':
      return (
        <Strip
          lead={flag.list ? `Moved to ${flag.list.name}.` : 'Moved to another list.'}
          text={flag.list ? undefined : "You can't open that list."}
          actions={[got('moved', 'Got it, moved')]}
        />
      )
    case 'recipients_changed':
      return (
        <Strip
          lead={flag.new_recipients && flag.new_recipients.length > 0 ? `Now for ${joinNames(flag.new_recipients.map((r) => r.name))}.` : 'The recipients changed.'}
          actions={[got('recipients_changed', 'Got it, recipients')]}
        />
      )
    case 'occasion_moved': {
      const to = occasionText(gift)
      return (
        <Strip
          lead={to ? `Moved to ${to}.` : 'Moved to another occasion.'}
          text={to ? undefined : "You can't see that occasion."}
          actions={[got('occasion_moved', 'Got it, occasion')]}
        />
      )
    }
    case 'no_longer_shared':
      return <Strip lead="No longer shared with you." actions={[got('no_longer_shared', 'Got it, no longer shared')]} />
    case 'restored':
      return (
        <Strip
          lead={flag.by ? `${flag.by.name} says they didn't get this.` : 'Marked as not received.'}
          actions={[got('restored', 'Got it, restored')]}
        />
      )
    default:
      return null
  }
}

/** Save and Cancel under an editor. */
function Save({ label, busy, unchanged, onSave, onCancel }: { label: string; busy: boolean; unchanged: boolean; onSave: () => void; onCancel: () => void }) {
  return (
    <div className="flex flex-wrap gap-2">
      <button type="button" className={button} disabled={busy || unchanged} aria-label={label} onClick={onSave}>
        Save
      </button>
      <button type="button" className={button} onClick={onCancel}>
        Cancel
      </button>
    </div>
  )
}

/** What the screen before "I won't get this" says: the item comes out of Giving, and others can get it. */
function outcome(gift: Gift): { statement: string; details: string[] } {
  const title = gift.quantity > 1 ? `${gift.title} (${gift.quantity})` : gift.title
  return { statement: `${title} comes out of Giving.`, details: ['Anyone else can get it now.'] }
}

/**
 * The screen before "I won't get this", on its own address so it is a history
 * entry. "Keep it" changes nothing and returns where the giver came from: the
 * item when the address says `?from=item`, the gift's row in Giving otherwise.
 * The return address is built here, never read from the URL.
 */
export function Unclaim({ giftId }: { giftId: string }) {
  return (
    <Resource<Gift> path={`/api/v2/gifts/${giftId}`}>
      {(gift) => {
        if (!gift.can_delete) return <NotFound />
        const fromItem = new URLSearchParams(window.location.search).get('from') === 'item'
        const { statement, details } = outcome(gift)
        return (
          <Confirm
            heading="Not getting this?"
            statement={statement}
            details={details}
            action="I won't get this"
            keepHref={fromItem ? `/items/${gift.item.id}` : `/giving#gift-${gift.id}`}
            onConfirm={async () => {
              await deleteGift(gift.id)
              replaceTo('/giving')
            }}
          />
        )
      }}
    </Resource>
  )
}
