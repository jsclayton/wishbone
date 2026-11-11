import { useState, type FormEvent, type ReactNode } from 'react'
import type { Item, NewItem } from './api'
import { DuplicateHint } from './DuplicateHint'
import { chosenFor, NOT_SURE, NOT_SURE_LABEL, notesToSave, pickable, startingNotes, type PageOption } from './options'
import { parsePrice, priceError, priceText } from './price'
import { notice } from './Picker'
import { button, field, primary, select } from './ui'

/** The currency a price is in when the user types a price and no currency. The server's default is the same. */
const DEFAULT_CURRENCY = 'USD'

/** The longest notes the server takes. */
const MAX_NOTES_LENGTH = 2000

/**
 * Drops the fields that have no value, so an add sends only what the user filled
 * in. The price fields stay, as null when the user left them empty. The server
 * reads a null price as no price chosen yet: an item with no price takes the
 * price of its first priced store link (docs/routes.md, The item price). The
 * add screen clears it again afterwards if the user left the price empty.
 */
function withoutNulls(input: NewItem): NewItem {
  const keep = ['price_minor', 'top_price_minor']
  return Object.fromEntries(Object.entries(input).filter(([key, value]) => value !== null || keep.includes(key))) as NewItem
}

/**
 * The form for an item: the title, the price (and the top of a range), the
 * notes, how many, and unlimited. It adds a new item or edits `initial`. An edit
 * sends every field, so what the user clears is cleared. An add sends only what
 * the user filled in, and the server's defaults do the rest. `onSave` returns
 * the words to show when the save was refused, or `null` when it worked.
 */
export function ItemForm({
  legend,
  submit,
  initial,
  prefill,
  options,
  duplicateOf,
  onSave,
  onCancel,
  cancelHref,
  children,
}: {
  legend: string
  submit: string
  initial?: Item
  /** The values an add starts with, such as a search found. An edit starts from `initial`. */
  prefill?: Pick<Partial<Item>, 'title' | 'notes' | 'price_minor' | 'top_price_minor' | 'currency'>
  /**
   * The choices a page offers, such as sizes. Each with values is a dropdown, and
   * the choices are saved into the notes ("Size: M"). Each without values starts
   * a line of the notes for the user to fill in.
   */
  options?: PageOption[]
  /**
   * Shows the duplicate hint of the add screen under the title, for the title as
   * it is typed: the list the add goes to, and the addresses of the pasted link.
   */
  duplicateOf?: { listId: string; urls: string[] }
  /** More of the form, above the Save button: what a search found, for an add. */
  children?: ReactNode
  onSave: (input: NewItem) => Promise<string | null>
  /** A Cancel button, for a form that opens in place of the screen's content. */
  onCancel?: () => void
  /** A Cancel link, for a form that is a screen of its own. */
  cancelHref?: string
}) {
  const start = initial ?? prefill
  const currency = start?.currency ?? DEFAULT_CURRENCY
  const [v, setV] = useState({
    title: start?.title ?? '',
    price: priceText(start?.price_minor ?? null, currency),
    priceMax: priceText(start?.top_price_minor ?? null, currency),
    currency,
    notes: initial ? (initial.notes ?? '') : startingNotes(prefill?.notes ?? '', options ?? []),
    quantity: String(initial?.quantity ?? 1),
    unlimited: initial?.is_unlimited ?? false,
  })
  const [chosen, setChosen] = useState(() => chosenFor(options ?? []))
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const set = <K extends keyof typeof v>(key: K, value: (typeof v)[K]) => setV((old) => ({ ...old, [key]: value }))

  async function submitForm(e: FormEvent) {
    e.preventDefault()
    const title = v.title.trim()
    const code = v.currency.trim().toUpperCase() || DEFAULT_CURRENCY
    const price = parsePrice(v.price, code)
    const priceMax = parsePrice(v.priceMax, code)
    const quantity = Number(v.quantity)
    if (title === '') return setError('Enter a title.')
    if (Number.isNaN(price) || Number.isNaN(priceMax)) return setError(priceError(code))
    if (priceMax !== undefined && price === undefined) return setError('Enter the price that the range starts at, or clear the top of the range.')
    if (!Number.isInteger(quantity) || quantity < 1) return setError('Enter a quantity of 1 or more.')

    const notes = (options?.length ? notesToSave(v.notes, options, chosen) : v.notes).trim()
    // The choice lines count toward the longest notes. Give the exact count, and keep what the user typed.
    const over = [...notes].length - MAX_NOTES_LENGTH
    if (over > 0) return setError(`The notes are too long. Take out ${over} ${over === 1 ? 'character' : 'characters'}, then save.`)

    const input: NewItem = {
      title,
      notes: notes === '' ? null : notes,
      price_minor: price ?? null,
      top_price_minor: priceMax ?? null,
      currency: price === undefined ? null : code,
      quantity,
      is_unlimited: v.unlimited,
    }
    setBusy(true)
    setError(null)
    try {
      const refused = await onSave(initial ? input : withoutNulls(input))
      if (refused) setError(refused)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <form onSubmit={submitForm} aria-label={legend} noValidate className="flex max-w-xl flex-col gap-4">
      <label className="flex flex-col gap-1 font-semibold">
        Title
        <input className={`${field} font-normal`} value={v.title} onChange={(e) => set('title', e.target.value)} />
      </label>
      {duplicateOf && <DuplicateHint listId={duplicateOf.listId} title={v.title} urls={duplicateOf.urls} />}
      <div className="flex gap-3">
        <label className="flex flex-1 flex-col gap-1 font-semibold">
          Price
          <input className={`${field} font-normal`} inputMode="decimal" value={v.price} onChange={(e) => set('price', e.target.value)} />
        </label>
        <label className="flex w-28 flex-col gap-1 font-semibold">
          Currency
          <input className={`${field} font-normal`} value={v.currency} maxLength={3} onChange={(e) => set('currency', e.target.value)} />
        </label>
      </div>
      <label className="flex flex-col gap-1 font-semibold">
        Up to (optional)
        <input className={`${field} font-normal`} inputMode="decimal" value={v.priceMax} onChange={(e) => set('priceMax', e.target.value)} />
      </label>
      {pickable(options ?? []).length > 0 && (
        <fieldset className="flex flex-col gap-1.5">
          <legend className="pb-1.5 font-semibold">Which one do you want?</legend>
          <div className="grid grid-cols-2 gap-2">
            {pickable(options ?? []).map((o, i) => (
              <div key={o.name} className="flex min-w-0 flex-col gap-1">
                <label htmlFor={`option-${i}`} className="text-base">
                  {o.name}
                </label>
                <select
                  id={`option-${i}`}
                  className={select}
                  value={chosen[o.name] ?? NOT_SURE}
                  onChange={(e) => setChosen((old) => ({ ...old, [o.name]: e.target.value }))}
                >
                  <option value={NOT_SURE}>{NOT_SURE_LABEL}</option>
                  {(o.values ?? []).map((value) => (
                    <option key={value} value={value}>
                      {value}
                    </option>
                  ))}
                </select>
              </div>
            ))}
          </div>
          <p className="text-mute">Your choices go in the notes, so givers see them.</p>
        </fieldset>
      )}
      <label className="flex flex-col gap-1 font-semibold">
        Notes
        <textarea className={`${field} font-normal`} rows={3} placeholder="Color, size, anything that helps" value={v.notes} onChange={(e) => set('notes', e.target.value)} />
      </label>
      <label className="flex flex-col gap-1 font-semibold">
        Quantity
        <input
          className={`${field} font-normal`}
          type="number"
          min={1}
          value={v.quantity}
          disabled={v.unlimited}
          onChange={(e) => set('quantity', e.target.value)}
        />
      </label>
      <label className="flex min-h-12 items-center gap-3 font-semibold">
        <input type="checkbox" className="size-6" checked={v.unlimited} onChange={(e) => set('unlimited', e.target.checked)} />
        Unlimited
      </label>
      {children}
      {error && (
        <p role="alert" className={notice}>
          {error}
        </p>
      )}
      <div className="flex flex-col gap-2 sm:flex-row">
        <button type="submit" className={`${primary} sm:flex-1`} disabled={busy}>
          {submit}
        </button>
        {onCancel && (
          <button type="button" className={button} onClick={onCancel}>
            Cancel
          </button>
        )}
        {cancelHref && (
          <a href={cancelHref} className={`${button} flex items-center justify-center`}>
            Cancel
          </a>
        )}
      </div>
    </form>
  )
}
