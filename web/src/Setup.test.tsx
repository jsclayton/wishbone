import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Setup } from './Setup'

function answer(status: number, code?: string, detail = 'Words from the server.') {
  const body = status < 400 ? { id: '7' } : { status, code, detail }
  const fetch = vi.fn<typeof globalThis.fetch>(async () => new Response(JSON.stringify(body), { status }))
  vi.stubGlobal('fetch', fetch)
  return fetch
}

async function fillIn(user: ReturnType<typeof userEvent.setup>, { year = '' } = {}) {
  await user.type(screen.getByLabelText('Setup code'), ' abcd-efgh-jkmn ')
  await user.type(screen.getByLabelText('First name'), ' Ada ')
  await user.type(screen.getByLabelText('Last name (optional)'), 'Lovelace')
  await user.selectOptions(screen.getByLabelText('Month'), 'February')
  await user.type(screen.getByLabelText('Day'), '29')
  if (year) await user.type(screen.getByLabelText('Year'), year)
}

afterEach(() => vi.unstubAllGlobals())

test('sends the code, the names, and the birthday as a partial date, and reports that setup is done', async () => {
  const user = userEvent.setup()
  const onDone = vi.fn<() => void>()
  const fetch = answer(201)
  render(<Setup onDone={onDone} />)

  await fillIn(user)
  await user.click(screen.getByRole('button', { name: 'Set up' }))

  expect(onDone).toHaveBeenCalledTimes(1)
  const init = fetch.mock.calls[0]?.[1]
  expect(JSON.parse(init?.body as string)).toEqual({
    setup_code: 'abcd-efgh-jkmn',
    name: 'Ada',
    last_name: 'Lovelace',
    birthday: { month: 2, day: 29, year: null },
  })
})

test('sends a birthday year when there is one, and leaves out an empty last name', async () => {
  const user = userEvent.setup()
  const fetch = answer(201)
  render(<Setup onDone={() => {}} />)

  await user.type(screen.getByLabelText('Setup code'), 'ABCD-EFGH-JKMN')
  await user.type(screen.getByLabelText('First name'), 'Ada')
  await user.selectOptions(screen.getByLabelText('Month'), 'December')
  await user.type(screen.getByLabelText('Day'), '10')
  await user.type(screen.getByLabelText('Year'), '1815')
  await user.click(screen.getByRole('button', { name: 'Set up' }))

  const body = JSON.parse(fetch.mock.calls[0]?.[1]?.body as string)
  expect(body.birthday).toEqual({ month: 12, day: 10, year: 1815 })
  expect(body).not.toHaveProperty('last_name')
})

test('asks for what is missing without calling the server', async () => {
  const user = userEvent.setup()
  const fetch = answer(201)
  render(<Setup onDone={() => {}} />)

  await user.click(screen.getByRole('button', { name: 'Set up' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('Enter the setup code')
  expect(fetch).not.toHaveBeenCalled()
})

test.each([
  [422, 'setup_code_invalid', "That setup code isn't right"],
  [429, 'rate_limited', 'Too many tries'],
  [422, 'invalid_input', 'Words from the server.'],
])('a %i %s tells the person, and keeps the form', async (status, code, message) => {
  const user = userEvent.setup()
  const onDone = vi.fn<() => void>()
  answer(status, code)
  render(<Setup onDone={onDone} />)

  await fillIn(user)
  await user.click(screen.getByRole('button', { name: 'Set up' }))

  expect(await screen.findByRole('alert')).toHaveTextContent(message)
  expect(onDone).not.toHaveBeenCalled()
  expect(screen.getByLabelText('First name')).toHaveValue(' Ada ')
  expect(screen.getByRole('button', { name: 'Set up' })).toBeEnabled()
})

test('setup that somebody else finished is over: it reports done', async () => {
  const user = userEvent.setup()
  const onDone = vi.fn<() => void>()
  answer(409, 'setup_complete')
  render(<Setup onDone={onDone} />)

  await fillIn(user)
  await user.click(screen.getByRole('button', { name: 'Set up' }))

  expect(onDone).toHaveBeenCalledTimes(1)
})

test('a server that fails says so', async () => {
  const user = userEvent.setup()
  answer(500, 'internal_error')
  render(<Setup onDone={() => {}} />)

  await fillIn(user)
  await user.click(screen.getByRole('button', { name: 'Set up' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong')
})
