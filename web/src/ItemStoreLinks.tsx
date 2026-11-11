import { useEffect, useRef, useState, type FormEvent } from 'react'
import { addStoreLink, getItem, MAX_STORE_LINKS, refreshPrices, removeStoreLink, setStoreLinkPrice, type Item, type StoreLink } from './api'
import { notice } from './Picker'
import { formatPrice, parsePrice, priceError, priceText } from './price'
import { button, field, link as linkClass, primary } from './ui'

/** The currency a price is in when the user types a price and no currency. The server's default is the same. */
const DEFAULT_CURRENCY = 'USD'

/** The date a price was captured, as "Sep 30". */
const shortDate = (iso: string) => new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })

/**
 * The "Where to buy" section of an item's page: its store links, each with the
 * price and the day it was captured, and for a user who can edit the list the
 * controls to add a link, set its price by hand, remove it, and refresh the
 * prices (docs/spec.md, Items, Store links). `onChange` gets the item as it
 * is after a change.
 */
export function StoreLinks({ item, onChange }: { item: Item; onChange: (item: Item) => void }) {
  const [adding, setAdding] = useState(false)
  // The store link whose price is being edited.
  const [pricing, setPricing] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [problem, setProblem] = useState<string | null>(null)
  const [status, setStatus] = useState<string | null>(null)

  if (!item.can_edit && item.store_links.length === 0) return null

  /** Loads the item again after a change to one of its store links. */
  async function reload() {
    const fresh = await getItem(item.id)
    if (fresh) onChange(fresh)
  }

  /** One change at a time. These controls use `aria-disabled`, so a pressed one keeps focus. */
  async function run(change: () => Promise<string | null>): Promise<boolean> {
    if (busy) return false
    setBusy(true)
    setProblem(null)
    setStatus(null)
    let done = false
    try {
      const refused = await change()
      if (refused) setProblem(refused)
      else {
        await reload()
        done = true
      }
    } catch {
      setProblem('Something went wrong. Try again.')
    }
    setBusy(false)
    return done
  }

  async function refresh() {
    if (refreshing || busy) return
    setProblem(null)
    setRefreshing(true)
    setStatus('Refreshing prices…')
    try {
      const res = await refreshPrices(item.id)
      if (!res.ok) {
        setStatus(null)
        setProblem(res.detail)
      } else {
        onChange(res.value)
        // The answer doesn't say which pages were read, and `captured_at` has
        // whole-second precision, so the one thing the app can say for sure is which
        // prices changed. A page that gave the same price or failed looks the same.
        const before = new Map(item.store_links.map((l) => [l.id, `${l.price_minor}/${l.currency}`]))
        const changed = res.value.store_links.filter((l) => before.get(l.id) !== `${l.price_minor}/${l.currency}`).length
        const total = res.value.store_links.length
        setStatus(
          changed === 0
            ? "No price changed. Either the prices are the same, or a page couldn't be read."
            : `${changed} of ${total} ${total === 1 ? 'price' : 'prices'} changed.`,
        )
      }
    } catch {
      setStatus(null)
      setProblem('Something went wrong. Try again.')
    }
    setRefreshing(false)
  }

  const full = item.store_links.length >= MAX_STORE_LINKS
  return (
    <section aria-labelledby="where-to-buy" className="flex max-w-xl flex-col gap-2">
      <h2 id="where-to-buy" className="text-[1.375rem] font-extrabold">
        Where to buy
      </h2>
      {item.store_links.length > 0 && (
        <div className="flex flex-col border-y border-hair font-mono">
          {item.store_links.map((l) => (
            <div key={l.id} className="flex flex-col border-t border-hair first:border-t-0">
              <div className="grid min-h-13 grid-cols-[minmax(0,1fr)_auto_4.5rem] items-center gap-x-3">
                <a href={l.url} target="_blank" rel="noreferrer noopener" className="inline-flex min-h-12 items-center font-semibold underline">
                  {l.store}
                </a>
                <span className="text-right">{l.price_minor !== null && l.currency !== null && formatPrice(l.price_minor, l.currency)}</span>
                <span className="text-right text-mute">
                  {l.price_minor !== null && l.captured_at && <time dateTime={l.captured_at}>{shortDate(l.captured_at)}</time>}
                </span>
              </div>
              {pricing === l.id ? (
                <div className="pb-3">
                  <PriceForm
                    link={l}
                    onCancel={() => setPricing(null)}
                    onSave={async (price) => {
                      const done = await run(async () => {
                        const res = await setStoreLinkPrice(item.id, l.id, price)
                        return res.ok ? null : res.detail
                      })
                      if (done) setPricing(null)
                      return null
                    }}
                  />
                </div>
              ) : (
                (l.can_edit || l.can_delete) && (
                  <div className="flex flex-wrap gap-x-4 font-sans">
                    {l.can_edit && (
                      <button
                        type="button"
                        className={`${linkClass} font-semibold`}
                        aria-label={`Edit price of ${l.store}`}
                        aria-disabled={busy}
                        onClick={() => {
                          setProblem(null)
                          setStatus(null)
                          setPricing(l.id)
                        }}
                      >
                        Edit price
                      </button>
                    )}
                    {l.can_delete && (
                      <button
                        type="button"
                        className={`${linkClass} font-semibold`}
                        aria-label={`Remove link ${l.store}`}
                        aria-disabled={busy}
                        onClick={() =>
                          void run(async () => {
                            await removeStoreLink(item.id, l.id)
                            return null
                          })
                        }
                      >
                        Remove link
                      </button>
                    )}
                  </div>
                )
              )}
            </div>
          ))}
        </div>
      )}
      {adding && (
        <LinkForm
          itemId={item.id}
          title={item.title}
          onCancel={() => setAdding(false)}
          onSaved={async () => {
            await reload()
            setAdding(false)
          }}
        />
      )}
      {status && <output className="font-semibold">{status}</output>}
      {problem && (
        <p role="alert" className={notice}>
          {problem}
        </p>
      )}
      {item.can_edit && (
        <div className="grid grid-cols-2 gap-2">
          <button
            type="button"
            className={button}
            disabled={full}
            onClick={() => {
              setProblem(null)
              setStatus(null)
              setAdding(true)
            }}
          >
            Add link
          </button>
          {item.can_refresh_prices && (
            <button type="button" className={button} aria-disabled={refreshing || busy} onClick={() => void refresh()}>
              {refreshing ? 'Refreshing…' : 'Refresh prices'}
            </button>
          )}
        </div>
      )}
      {item.can_edit && full && <p className="text-mute">An item can have {MAX_STORE_LINKS} store links. Remove one to add another.</p>}
    </section>
  )
}

/**
 * Adds a store link: the address. The server reads the page for its price, which
 * can take a while, and a read that fails still adds the link, with no price. A
 * link from a blocked store, and the hourly limit, come back as words here.
 */
function LinkForm({
  itemId,
  title,
  onSaved,
  onCancel,
}: {
  itemId: string
  title: string
  onSaved: () => Promise<void>
  onCancel: () => void
}) {
  const reading = useRef<AbortController | null>(null)
  // Leaving the form, however it closes, stops a read that is still running.
  useEffect(() => () => reading.current?.abort(), [])
  const [url, setUrl] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    const typed = url.trim()
    if (typed === '') return setError('Enter the link.')
    setBusy(true)
    setError(null)
    const controller = new AbortController()
    reading.current = controller
    try {
      const res = await addStoreLink(itemId, typed, controller.signal)
      if (!res.ok) setError(res.detail)
      else await onSaved()
    } catch {
      // Cancel stopped the read: the form is gone.
      if (controller.signal.aborted) return
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <form onSubmit={submit} aria-label={`Add a link to ${title}`} noValidate className="flex flex-col gap-3">
      <label className="flex flex-col gap-1 font-semibold">
        Link
        <input className={`${field} font-normal`} value={url} inputMode="url" autoCapitalize="none" onChange={(e) => setUrl(e.target.value)} />
      </label>
      {busy && <output>Reading the link…</output>}
      {error && (
        <p role="alert" className={notice}>
          {error}
        </p>
      )}
      <div className="flex flex-wrap gap-3">
        <button type="submit" className={primary} disabled={busy}>
          Save link
        </button>
        <button
          type="button"
          className={button}
          onClick={() => {
            reading.current?.abort()
            onCancel()
          }}
        >
          Cancel
        </button>
      </div>
    </form>
  )
}

/** Sets or clears one store link's price by hand. An empty price clears it. */
function PriceForm({
  link,
  onSave,
  onCancel,
}: {
  link: StoreLink
  /** Returns nothing: the caller shows what went wrong. */
  onSave: (price: { price_minor: number | null; currency?: string }) => Promise<null>
  onCancel: () => void
}) {
  const start = link.currency ?? DEFAULT_CURRENCY
  const [price, setPrice] = useState(priceText(link.price_minor, start))
  const [currency, setCurrency] = useState(start)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    const code = currency.trim().toUpperCase() || DEFAULT_CURRENCY
    const minor = parsePrice(price, code)
    if (Number.isNaN(minor)) return setError(priceError(code))
    setBusy(true)
    setError(null)
    try {
      await onSave(minor === undefined ? { price_minor: null } : { price_minor: minor, currency: code })
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <form onSubmit={submit} aria-label={`Edit price of ${link.store}`} noValidate className="flex flex-col gap-3 font-sans">
      <div className="flex gap-3">
        <label className="flex flex-1 flex-col gap-1 font-semibold">
          Price
          <input className={`${field} font-normal`} inputMode="decimal" value={price} onChange={(e) => setPrice(e.target.value)} />
        </label>
        <label className="flex w-28 flex-col gap-1 font-semibold">
          Currency
          <input className={`${field} font-normal`} value={currency} maxLength={3} onChange={(e) => setCurrency(e.target.value)} />
        </label>
      </div>
      <p className="text-mute">Leave the price empty to clear it.</p>
      {error && (
        <p role="alert" className={notice}>
          {error}
        </p>
      )}
      <div className="flex flex-wrap gap-3">
        <button type="submit" className={primary} disabled={busy}>
          Save price
        </button>
        <button type="button" className={button} onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  )
}
