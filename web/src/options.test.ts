import { chosenFor, headerLines, NOT_SURE, notesToSave, pickable, startingNotes, type PageOption } from './options'

const size: PageOption = { name: 'Size', values: ['S', 'M', 'L'], selected: 'M' }
const color: PageOption = { name: 'Color', values: ['Black', 'Navy'] }
const names: PageOption[] = [{ name: 'Size' }, { name: 'Color' }]

test('an option with values is a dropdown, and one without is a header line', () => {
  expect(pickable([size, color, ...names])).toEqual([size, color])
  expect(headerLines([size, color, ...names])).toEqual(['Size:', 'Color:'])
})

test('a dropdown starts on the value the page picks, otherwise Not sure', () => {
  expect(chosenFor([size, color])).toEqual({ Size: 'M', Color: NOT_SURE })
  expect(NOT_SURE).toBe('')
})

test('a page value that is literally "Not sure" can be chosen', () => {
  const fit: PageOption = { name: 'Fit', values: ['Not sure', 'Slim'] }
  expect(notesToSave('', [fit], { Fit: 'Not sure' })).toBe('Fit: Not sure')
  expect(notesToSave('', [fit], { Fit: NOT_SURE })).toBe('')
})

test('notes start with a line for each name that has no values', () => {
  expect(startingNotes('', [size, ...names])).toBe('Size:\nColor:')
  expect(startingNotes('', [size, color])).toBe('')
  expect(startingNotes('A gift', names)).toBe('A gift')
})

test('chosen values go first in the notes, as Name: value lines', () => {
  const chosen = { Size: 'M', Color: 'Navy' }
  expect(notesToSave('', [size, color], chosen)).toBe('Size: M\nColor: Navy')
  expect(notesToSave('The grey one', [size, color], chosen)).toBe('Size: M\nColor: Navy\nThe grey one')
})

test('Not sure writes nothing', () => {
  expect(notesToSave('', [size, color], { Size: NOT_SURE, Color: NOT_SURE })).toBe('')
  expect(notesToSave('Typed', [size, color], { Size: NOT_SURE, Color: 'Navy' })).toBe('Color: Navy\nTyped')
})

test('a header line left unchanged is dropped, and one that was filled in is kept', () => {
  expect(notesToSave('Size:\nColor:', names, {})).toBe('')
  expect(notesToSave('Size: M\nColor:', names, {})).toBe('Size: M')
  expect(notesToSave('Size:  \nthe grey one', names, {})).toBe('the grey one')
})

test('text the member typed is kept as it is', () => {
  expect(notesToSave('  Gift wrap please\n\nThanks  ', [], {})).toBe('Gift wrap please\n\nThanks')
})

test('a choice the member also typed is not written twice', () => {
  expect(notesToSave('Size: M\nplease', [size], { Size: 'M' })).toBe('Size: M\nplease')
})

test('only a plain "Size:" line is a header: other text with that word stays', () => {
  expect(notesToSave('Size: 8 or 9', names, {})).toBe('Size: 8 or 9')
})
