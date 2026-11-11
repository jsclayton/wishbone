import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Confirm } from './Confirm'

const props = {
  heading: 'Delete this item?',
  statement: 'Wireless headphones comes off your list.',
  action: 'Delete this item',
  keepHref: '/list',
}

afterEach(() => vi.restoreAllMocks())

test('shows the statement, the exact action, and Keep it below it at the same size', () => {
  render(<Confirm {...props} onConfirm={async () => {}} details={["People won't see it any more."]} />)
  expect(screen.getByRole('heading', { level: 1, name: 'Delete this item?' })).toBeInTheDocument()
  expect(screen.getByText('Wireless headphones comes off your list.')).toBeInTheDocument()
  expect(screen.getByText("People won't see it any more.")).toBeInTheDocument()
  const action = screen.getByRole('button', { name: 'Delete this item' })
  const keep = screen.getByRole('link', { name: 'Keep it' })
  expect(keep).toHaveAttribute('href', '/list')
  expect(action.compareDocumentPosition(keep) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  const height = (el: HTMLElement) => el.className.split(' ').find((c) => c.startsWith('min-h-'))
  expect(height(keep)).toBe(height(action))
})

test('the keep wording can name what stays', () => {
  render(<Confirm {...props} onConfirm={async () => {}} keepLabel="Keep it in Received" />)
  expect(screen.getByRole('link', { name: 'Keep it in Received' })).toBeInTheDocument()
})

test('runs the action once on the tap, and says so if it fails', async () => {
  const onConfirm = vi.fn<() => Promise<void>>(async () => {
    throw new Error('nope')
  })
  render(<Confirm {...props} onConfirm={onConfirm} />)
  await userEvent.click(screen.getByRole('button', { name: 'Delete this item' }))
  expect(onConfirm).toHaveBeenCalledTimes(1)
  expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong. Try again.')
  expect(screen.getByRole('button', { name: 'Delete this item' })).toBeEnabled()
})
