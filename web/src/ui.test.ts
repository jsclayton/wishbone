import { describe, expect, it } from 'vitest'
import { button, filePicker, primary } from './ui'

// The boards draw buttons at weight 800. Heights are minimums, so long labels wrap.
describe('button classes', () => {
  it('primary is 62px high with 20px text at weight 800', () => {
    expect(primary).toContain('min-h-15.5')
    expect(primary).toContain('text-xl')
    expect(primary).toContain('font-extrabold')
    expect(primary).not.toContain('font-semibold')
  })

  it('button is 56px high with 18px text at weight 800', () => {
    expect(button).toContain('min-h-14')
    expect(button).toContain('text-lg')
    expect(button).toContain('font-extrabold')
    expect(button).not.toContain('font-semibold')
  })

  it('the file picker button matches button', () => {
    expect(filePicker).toContain('file:min-h-14')
    expect(filePicker).toContain('file:text-lg')
    expect(filePicker).toContain('file:font-extrabold')
    expect(filePicker).not.toContain('file:font-semibold')
  })
})
