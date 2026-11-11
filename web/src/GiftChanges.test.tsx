import { render } from '@testing-library/react'
import type { GiftChange } from './api'
import { ChangeList, describeChange } from './GiftChanges'
import { formatPrice } from './price'

const usd = (minor: number, top: number | null = null) => ({ price_minor: minor, top_price_minor: top, currency: 'USD' })

/** The text of one change as it shows on screen. */
const line = (c: GiftChange) => render(<ChangeList changes={[c]} />).container.textContent

test('a price change shows the old and the new price', () => {
  expect(line({ field: 'price', old: usd(5000), new: usd(4500) })).toBe(`Price went from ${formatPrice(5000, 'USD')} to ${formatPrice(4500, 'USD')}.`)
})

test('a store link price change names the store without www', () => {
  const c: GiftChange = { field: 'store_link_price', url: 'https://www.shop.example/kettle', old: usd(5000), new: usd(4250) }
  expect(line(c)).toBe(`Price at shop.example went from ${formatPrice(5000, 'USD')} to ${formatPrice(4250, 'USD')}.`)
})

test('a price that appeared or went away says so', () => {
  expect(line({ field: 'price', old: null, new: usd(900) })).toBe(`Price went from no price to ${formatPrice(900, 'USD')}.`)
  expect(line({ field: 'price', old: usd(900), new: null })).toBe(`Price went from ${formatPrice(900, 'USD')} to no price.`)
})

test('a price range shows both ends', () => {
  expect(line({ field: 'price', old: usd(1000), new: usd(1000, 2000) })).toBe(
    `Price went from ${formatPrice(1000, 'USD')} to ${formatPrice(1000, 'USD')}–${formatPrice(2000, 'USD')}.`,
  )
})

test('each price is mono, and the words around them are not', () => {
  const { container } = render(<ChangeList changes={[{ field: 'price', old: usd(1000), new: usd(1000, 2000) }]} />)
  expect([...container.querySelectorAll('.font-mono')].map((e) => e.textContent)).toEqual([
    formatPrice(1000, 'USD'),
    formatPrice(1000, 'USD'),
    formatPrice(2000, 'USD'),
  ])
})

test('a removed store link names the store', () => {
  expect(describeChange({ field: 'store_link_removed', url: 'https://a.example/x' })).toBe('The link to a.example was removed.')
})

test('text fields show the old and new text', () => {
  expect(describeChange({ field: 'title', old: 'Kettle', new: 'Teapot' })).toBe('Title went from “Kettle” to “Teapot”.')
  expect(describeChange({ field: 'notes', old: null, new: 'blue' })).toBe('Notes went from (empty) to “blue”.')
  expect(describeChange({ field: 'quantity', old: 1, new: 3 })).toBe('Quantity went from 1 to 3.')
  expect(describeChange({ field: 'is_unlimited', old: false, new: true })).toBe('Unlimited went from no to yes.')
  expect(describeChange({ field: 'primary_image' })).toBe('The picture changed.')
})

test('several changes show as a list, and one shows inline', () => {
  const many = render(
    <ChangeList
      changes={[
        { field: 'title', old: 'A', new: 'B' },
        { field: 'quantity', old: 1, new: 2 },
      ]}
    />,
  )
  expect(many.container.querySelectorAll('li')).toHaveLength(2)
  expect(render(<ChangeList changes={[{ field: 'primary_image' }]} />).container.querySelector('li')).toBeNull()
})
