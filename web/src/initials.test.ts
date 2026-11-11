import { initialsOf } from './initials'

describe('initialsOf', () => {
  test.each([
    ['Grace', 'Hopper', 'GH'],
    ['Grace', null, 'G'],
    ['Grace', undefined, 'G'],
    ['Grace', '', 'G'],
    ['  grace ', ' hopper ', 'GH'],
    ['"Gracie"', "O'Brien", 'GO'],
    ['élodie', 'Ünal', 'ÉÜ'],
    ['Zoë', null, 'Z'],
    ['李', '雷', '李雷'],
    ['42', null, '4'],
    ['', null, ''],
  ])('%j and %j give %j', (first, last, expected) => {
    expect(initialsOf(first, last)).toBe(expected)
  })

  test('keeps the combining marks of a letter', () => {
    // "e" followed by a combining acute accent.
    expect(initialsOf('émile', null)).toBe('É')
  })
})
