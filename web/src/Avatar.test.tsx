import { render } from '@testing-library/react'
import { Avatar } from './Avatar'

describe('Avatar', () => {
  test('shows the photo when there is one', () => {
    const { container } = render(<Avatar initials="NM" url="/api/v2/images/1" />)
    const img = container.querySelector('img')
    expect(img).toHaveAttribute('src', '/api/v2/images/1')
    expect(img).toHaveAttribute('alt', '')
    expect(img).toHaveClass('rounded-full')
  })

  test('shows the initials from the server when there is no photo', () => {
    const { container } = render(<Avatar initials="NM" />)
    expect(container.querySelector('img')).toBeNull()
    expect(container.firstElementChild).toHaveAttribute('data-initials', 'NM')
    expect(container.firstElementChild).toHaveClass('rounded-full', 'bg-[#4D5A66]', 'text-white')
  })

  test.each([24, 32, 40, 52, 72, 88, 104, 180] as const)('draws initials at weight 800 at %ipx', (size) => {
    const { container } = render(<Avatar initials="NM" size={size} />)
    expect(container.firstElementChild).toHaveClass('font-extrabold')
    expect(container.firstElementChild).not.toHaveClass('font-semibold')
  })

  test('leaves a photo avatar without a font weight', () => {
    const { container } = render(<Avatar initials="NM" url="/api/v2/images/1" />)
    expect(container.querySelector('img')?.className).toBe('size-10 text-sm shrink-0 rounded-full object-cover')
  })

  test('is hidden from screen readers and adds nothing to the page text', () => {
    const { container } = render(<Avatar initials="U" />)
    expect(container.firstElementChild).toHaveAttribute('aria-hidden', 'true')
    expect(container).toHaveTextContent('')
  })

  test('takes its size in pixels', () => {
    const { container, rerender } = render(<Avatar initials="U" size={24} />)
    expect(container.firstElementChild).toHaveClass('size-6')
    rerender(<Avatar initials="U" size={104} />)
    expect(container.firstElementChild).toHaveClass('size-26')
    rerender(<Avatar initials="U" size={180} />)
    expect(container.firstElementChild).toHaveClass('size-45', 'text-[4.25rem]')
    rerender(<Avatar initials="U" />)
    expect(container.firstElementChild).toHaveClass('size-10')
  })

  it('shrinks to the 104 look on short screens only when asked', () => {
    const { container, rerender } = render(<Avatar initials="AB" size={180} />)
    expect(container.firstElementChild).not.toHaveClass('[@media(height<700px)]:size-26')
    rerender(<Avatar initials="AB" size={180} shrinkOnShortScreen />)
    expect(container.firstElementChild).toHaveClass('[@media(height<700px)]:size-26', '[@media(height<700px)]:text-4xl')
  })
})
