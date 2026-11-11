import { render, screen } from '@testing-library/react'
import { Menu } from './Menu'
import { sourceUrl } from './source'
import { ada } from './test/fake'

function serveHealth(answer: () => Response) {
  const fetch = vi.fn<typeof globalThis.fetch>(async () => answer())
  vi.stubGlobal('fetch', fetch)
  return fetch
}

afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

describe('the source code link', () => {
  test('goes to the repository, and shows the version the server reports', async () => {
    const fetch = serveHealth(() => Response.json({ status: 'ok', version: '2.1.0+abc1234', commit: 'abc1234' }))
    render(<Menu me={ada} onLoggedOut={() => {}} />)

    const link = screen.getByRole('link', { name: 'Source code' })
    expect(link).toHaveAttribute('href', 'https://github.com/jsclayton/wishbone')
    expect(link).toHaveAttribute('href', sourceUrl)
    expect(await screen.findByText('Version 2.1.0+abc1234')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/api/v2/health', expect.anything())
  })

  test('stays, with no version, when the server cannot say it', async () => {
    const fetch = serveHealth(() => new Response(null, { status: 503 }))
    render(<Menu me={ada} onLoggedOut={() => {}} />)

    expect(screen.getByRole('link', { name: 'Source code' })).toHaveAttribute('href', sourceUrl)
    await vi.waitFor(() => expect(fetch).toHaveBeenCalled())
    expect(screen.queryByText(/^Version/)).not.toBeInTheDocument()
  })
})
