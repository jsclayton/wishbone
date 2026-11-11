import type { ReactNode } from 'react'
import type { GiftChange, GiftPrice } from './api'
import { formatPrice } from './price'

function store(url: string | undefined): string {
  if (!url) return 'the store'
  try {
    return new URL(url).hostname.replace(/^www\./, '')
  } catch {
    return url
  }
}

function isPrice(v: GiftChange['old']): v is GiftPrice {
  return typeof v === 'object' && v !== null
}

/** A price in the diff: mono, "$10.00–$20.00" for a range, "no price" for none. */
function price(p: GiftChange['old']): ReactNode {
  if (!isPrice(p)) return 'no price'
  const low = <span className="font-mono">{formatPrice(p.price_minor, p.currency)}</span>
  if (p.top_price_minor === null) return low
  return (
    <>
      {low}–<span className="font-mono">{formatPrice(p.top_price_minor, p.currency)}</span>
    </>
  )
}

function text(v: GiftChange['old']): string {
  return typeof v === 'string' && v !== '' ? `“${v}”` : '(empty)'
}

const yesNo = (v: GiftChange['old']) => (v === true ? 'yes' : 'no')

/** One sentence of the diff behind "Changed since you picked it". */
export function describeChange(c: GiftChange): ReactNode {
  switch (c.field) {
    case 'price':
      return (
        <>
          Price went from {price(c.old)} to {price(c.new)}.
        </>
      )
    case 'store_link_price':
      return (
        <>
          Price at {store(c.url)} went from {price(c.old)} to {price(c.new)}.
        </>
      )
    case 'store_link_removed':
      return `The link to ${store(c.url)} was removed.`
    case 'title':
      return `Title went from ${text(c.old)} to ${text(c.new)}.`
    case 'notes':
      return `Notes went from ${text(c.old)} to ${text(c.new)}.`
    case 'quantity':
      return `Quantity went from ${String(c.old)} to ${String(c.new)}.`
    case 'is_unlimited':
      return `Unlimited went from ${yesNo(c.old)} to ${yesNo(c.new)}.`
    case 'primary_image':
      return 'The picture changed.'
  }
}

/** The diff behind a changed flag: one sentence inline, or a list when there are several. */
export function ChangeList({ changes }: { changes: GiftChange[] }) {
  if (changes.length === 1 && changes[0]) return <>{describeChange(changes[0])}</>
  return (
    <ul className="mt-1">
      {changes.map((c, i) => (
        <li key={`${c.field}:${c.url ?? ''}:${i}`}>{describeChange(c)}</li>
      ))}
    </ul>
  )
}
