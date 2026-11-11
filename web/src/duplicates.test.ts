import type { Item } from './api'
import { kettle } from './test/fake'
import { findDuplicate, sameTitle } from './duplicates'

const item = (id: string, title: string, urls: string[] = []): Item => ({
  ...kettle,
  id,
  title,
  store_links: urls.map((url, i) => ({
    id: `${id}${i}`,
    url,
    store: new URL(url).hostname,
    price_minor: null,
    currency: null,
    captured_at: null,
    image_url: null,
    created_at: '2026-11-08T12:30:00Z',
    can_edit: true,
    can_delete: true,
  })),
})

describe('sameTitle', () => {
  test('ignores case and extra spaces', () => {
    expect(sameTitle('  A   KETTLE ', 'a kettle')).toBe(true)
    expect(sameTitle('A\tkettle', 'A kettle')).toBe(true)
    expect(sameTitle('Kettle', 'A kettle')).toBe(false)
  })

  test('is not fooled by an empty title', () => {
    expect(sameTitle('', '')).toBe(false)
    expect(sameTitle('   ', 'A kettle')).toBe(false)
  })
})

describe('findDuplicate', () => {
  const items = [item('1', 'Grill', ['https://a.example/grill']), item('2', 'Kettle', ['https://b.example/k', 'https://c.example/k'])]

  test('matches by the title', () => {
    expect(findDuplicate(items, ' kettle ', [])?.id).toBe('2')
  })

  test("matches by any of an item's store link addresses", () => {
    expect(findDuplicate(items, '', ['https://c.example/k'])?.id).toBe('2')
    expect(findDuplicate(items, 'Something else', ['https://a.example/grill'])?.id).toBe('1')
  })

  test('finds nothing for a new title and a new address', () => {
    expect(findDuplicate(items, 'Mug', ['https://d.example/m'])).toBeNull()
    expect(findDuplicate([], 'Mug', [])).toBeNull()
  })

  test('takes the first item when several match', () => {
    expect(findDuplicate([item('1', 'Kettle'), item('2', 'Kettle')], 'kettle', [])?.id).toBe('1')
  })

  test('prefers the item with the address over the item with the title', () => {
    const both = [item('1', 'Kettle'), item('2', 'Other', ['https://b.example/k'])]
    expect(findDuplicate(both, 'Kettle', ['https://b.example/k'])?.id).toBe('2')
  })
})
