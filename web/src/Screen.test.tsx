import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CantReach, NotFound, Screen } from './Screen'

afterEach(() => {
  vi.restoreAllMocks()
  window.history.replaceState(null, '', '/')
})

describe('the top row', () => {
  test('has Wishbone, which goes to Home, and Menu', () => {
    render(<Screen title="Lists">x</Screen>)
    expect(screen.getByRole('link', { name: 'Wishbone' })).toHaveAttribute('href', '/')
    expect(screen.getByRole('link', { name: 'Menu' })).toHaveAttribute('href', '/menu')
  })

  test('leaves out Menu when the screen asks, as Menu itself does', () => {
    render(
      <Screen title="Menu" menu={false}>
        x
      </Screen>,
    )
    expect(screen.queryByRole('link', { name: 'Menu' })).not.toBeInTheDocument()
  })

  test('has no Back button in a browser, which has its own', () => {
    window.history.replaceState(null, '', '/lists')
    render(<Screen title="Lists">x</Screen>)
    expect(screen.queryByRole('button', { name: 'Back' })).not.toBeInTheDocument()
  })

  describe('in the installed app', () => {
    beforeEach(() => {
      vi.spyOn(window, 'matchMedia').mockImplementation((query: string) => ({ matches: query === '(display-mode: standalone)' }) as MediaQueryList)
    })

    test('shows Back on every screen but Home', () => {
      window.history.replaceState(null, '', '/lists')
      render(<Screen title="Lists">x</Screen>)
      expect(screen.getByRole('button', { name: 'Back' })).toBeInTheDocument()
    })

    test('has no Back on Home', () => {
      render(<Screen title="Home">x</Screen>)
      expect(screen.queryByRole('button', { name: 'Back' })).not.toBeInTheDocument()
    })

    test('Back with no earlier in-app screen goes Home', async () => {
      window.history.replaceState(null, '', '/lists')
      render(<Screen title="Lists">x</Screen>)
      await userEvent.click(screen.getByRole('button', { name: 'Back' }))
      expect(window.location.pathname).toBe('/')
    })
  })
})

test('the heading is the title, with an action at its end', () => {
  render(
    <Screen title="Lists" action={<a href="/lists/new">New list</a>}>
      x
    </Screen>,
  )
  expect(screen.getByRole('heading', { level: 1, name: 'Lists' })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'New list' })).toBeInTheDocument()
})

test("can't find that page says the same for a missing page and a hidden one", () => {
  render(<NotFound />)
  expect(screen.getByRole('heading', { level: 1, name: "We can't find that page" })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Go to Home' })).toHaveAttribute('href', '/')
})

test("can't reach Wishbone offers a retry", async () => {
  const onRetry = vi.fn<() => void>()
  render(<CantReach onRetry={onRetry} retrying={false} />)
  expect(screen.getByRole('heading', { level: 1, name: "Can't reach Wishbone" })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
  expect(onRetry).toHaveBeenCalledOnce()
})

test('Try again waits while a retry runs', () => {
  render(<CantReach onRetry={() => {}} retrying />)
  expect(screen.getByRole('button', { name: 'Try again' })).toBeDisabled()
})
