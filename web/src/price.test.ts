import { formatPrice, pagePrice, parsePrice, priceError, priceText, rowPrice } from './price'

describe('parsePrice', () => {
  test('turns what was typed into minor units', () => {
    expect(parsePrice('12.50', 'USD')).toBe(1250)
    expect(parsePrice(' 12 ', 'USD')).toBe(1200)
    expect(parsePrice('1,299', 'USD')).toBe(129900)
    expect(parsePrice('1,299.9', 'USD')).toBe(129990)
    expect(parsePrice('1200', 'JPY')).toBe(1200)
  })

  test('is undefined for an empty field and NaN for anything else that is not a price', () => {
    expect(parsePrice('  ', 'USD')).toBeUndefined()
    for (const bad of ['abc', '12.345', '-1', '1,23', '1.2.3', '$5']) expect(parsePrice(bad, 'USD')).toBeNaN()
    expect(parsePrice('12.5', 'JPY')).toBeNaN() // a yen has no decimals
  })
})

describe('priceText and priceError', () => {
  test('write a price as a field shows it', () => {
    expect(priceText(1250, 'USD')).toBe('12.50')
    expect(priceText(1200, 'JPY')).toBe('1200')
    expect(priceText(null, 'USD')).toBe('')
  })

  test('tell how many decimals the currency has', () => {
    expect(priceError('USD')).toBe('Enter the price as a number with up to 2 decimal places, like 12.50.')
    expect(priceError('JPY')).toBe('Enter the price as a whole number, like 1200.')
  })
})

describe('prices as the screens draw them', () => {
  const none = { price_minor: null, top_price_minor: null, currency: null }

  test('a list row is short: cents only when there are some, a range with a dash', () => {
    expect(rowPrice({ price_minor: 19900, top_price_minor: null, currency: 'USD' })).toBe('$199')
    expect(rowPrice({ price_minor: 19950, top_price_minor: null, currency: 'USD' })).toBe('$199.50')
    expect(rowPrice({ price_minor: 10000, top_price_minor: 20000, currency: 'USD' })).toBe('$100-200')
    expect(rowPrice(none)).toBeNull()
  })

  test("an item's page is exact, and a range says to", () => {
    expect(pagePrice({ price_minor: 19900, top_price_minor: null, currency: 'USD' })).toBe('$199.00')
    expect(pagePrice({ price_minor: 10000, top_price_minor: 20000, currency: 'USD' })).toBe('$100.00 to $200.00')
    expect(pagePrice(none)).toBeNull()
    expect(formatPrice(1200, 'JPY')).toMatch(/1,200/)
  })
})
