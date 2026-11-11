/** Prices are integers in the currency's minor unit (ISO 4217): 2 decimal places for USD and EUR, 0 for JPY, 3 for KWD. */

/** How many decimal places the currency's minor unit has: 2 for USD, 0 for JPY. */
export function fractionDigits(currency: string): number {
  try {
    return new Intl.NumberFormat(undefined, { style: 'currency', currency }).resolvedOptions().maximumFractionDigits ?? 2
  } catch {
    return 2
  }
}

/** The price in the currency's minor unit, as the user would write it. */
export function formatPrice(minor: number, currency: string): string {
  const digits = fractionDigits(currency)
  try {
    return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(minor / 10 ** digits)
  } catch {
    return `${(minor / 10 ** digits).toFixed(digits)} ${currency}`
  }
}

/** A price as a list draws it: "$199", with cents only when there are some. */
function shortPrice(minor: number, currency: string): string {
  const digits = fractionDigits(currency)
  const unit = 10 ** digits
  const whole = minor % unit === 0
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency,
      minimumFractionDigits: whole ? 0 : undefined,
      maximumFractionDigits: whole ? 0 : undefined,
    }).format(minor / unit)
  } catch {
    return `${(minor / unit).toFixed(digits)} ${currency}`
  }
}

/** What a price has: the amounts and the currency, as an item holds them. */
type Priced = { price_minor: number | null; top_price_minor: number | null; currency: string | null }

/** The price on the right of a row: the price or the range, short. `null` when there is no price. */
export function rowPrice(item: Priced): string | null {
  if (item.price_minor === null || item.currency === null) return null
  const low = shortPrice(item.price_minor, item.currency)
  if (item.top_price_minor === null) return low
  return `${low.replace(/\D*$/, '')}-${shortPrice(item.top_price_minor, item.currency).replace(/^\D*/, '')}`
}

/** The price on an item's page: "$199.00", or "$199.00 to $249.00" for a range. `null` when there is no price. */
export function pagePrice(item: Priced): string | null {
  if (item.price_minor === null || item.currency === null) return null
  const low = formatPrice(item.price_minor, item.currency)
  return item.top_price_minor === null ? low : `${low} to ${formatPrice(item.top_price_minor, item.currency)}`
}

/**
 * Parses what a user typed into minor units. Returns undefined for an empty
 * field and NaN for anything else that is not a price. "," groups thousands
 * ("1,299" is 1299), "." is the decimal mark, and more decimals than the
 * currency has is an error, not a rounding.
 */
export function parsePrice(text: string, currency: string): number | undefined {
  const t = text.trim()
  if (t === '') return undefined
  const m = /^(\d+|\d{1,3}(?:,\d{3})+)(?:\.(\d+))?$/.exec(t)
  if (!m) return NaN
  const digits = fractionDigits(currency)
  const fraction = m[2] ?? ''
  if (fraction.length > digits) return NaN
  const whole = (m[1] ?? '').replaceAll(',', '')
  return Number(whole + fraction.padEnd(digits, '0'))
}

/** A price in minor units as a field shows it: "12.50". Empty for no price. */
export function priceText(minor: number | null, currency: string): string {
  if (minor === null) return ''
  const digits = fractionDigits(currency)
  return (minor / 10 ** digits).toFixed(digits)
}

/** What to tell a user whose price text `parsePrice` refused, for this currency. */
export function priceError(currency: string): string {
  const digits = fractionDigits(currency)
  return digits === 0 ? 'Enter the price as a whole number, like 1200.' : `Enter the price as a number with up to ${digits} decimal places, like 12.50.`
}
