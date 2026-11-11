import { renderToStaticMarkup } from 'react-dom/server'
import { Markdown } from './Markdown'

const html = (text: string) => renderToStaticMarkup(<Markdown text={text} />)

describe('Markdown', () => {
  test('escapes raw HTML instead of rendering it', () => {
    const out = html('<script>alert(1)</script> <img src=x onerror=alert(1)> <b>bold</b>')
    expect(out).not.toContain('<script')
    expect(out).not.toContain('<img')
    expect(out).not.toContain('<b>')
    expect(out).toContain('&lt;script&gt;alert(1)&lt;/script&gt;')
    expect(out).toContain('&lt;img src=x onerror=alert(1)&gt;')
  })

  test('escapes HTML inside formatting and link text', () => {
    const out = html('**<i>x</i>** and [<u>y</u>](https://example.com) and `<div>`')
    expect(out).not.toMatch(/<(i|u|div)>/)
    expect(out).toContain('&lt;i&gt;x&lt;/i&gt;')
    expect(out).toContain('&lt;u&gt;y&lt;/u&gt;')
    expect(out).toContain('<code>&lt;div&gt;</code>')
  })

  test('does not render a link with an unsafe scheme', () => {
    for (const url of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'data:text/html,hi', 'vbscript:x']) {
      const out = html(`[click](${url})`)
      expect(out).not.toContain('<a')
      expect(out).not.toContain('href')
      expect(out).toContain('click')
    }
  })

  test('renders an https link that opens safely and cannot break out of the attribute', () => {
    const out = html('[shop](https://example.com/a?b=1&c="2")')
    expect(out).toMatch(/<a [^>]*href="https:\/\/example\.com\/a\?b=1&amp;c=&quot;2&quot;"/)
    expect(out).toContain('rel="noopener noreferrer nofollow"')
    expect(out).toContain('>shop</a>')
  })

  test('underlines a link, so it can be told from the sentence around it', () => {
    expect(html('see [shop](https://example.com/) now')).toMatch(/<a [^>]*class="underline"/)
  })

  test('renders bold, italic, and code', () => {
    expect(html('**bold** *it* _it2_ `c`')).toBe(
      '<p><strong>bold</strong> <em>it</em> <em>it2</em> <code>c</code></p>',
    )
  })

  test('renders paragraphs, line breaks, and lists', () => {
    expect(html('one\ntwo\n\nthree')).toBe('<p>one<br/>two</p><p>three</p>')
    expect(html('- a\n- b\n\n1. x\n2. y')).toBe(
      '<ul class="list-disc pl-6"><li>a</li><li>b</li></ul><ol class="list-decimal pl-6"><li>x</li><li>y</li></ol>',
    )
  })

  test('leaves stray markers and empty text alone', () => {
    expect(html('2 * 3 * 4 and snake_case_name')).toBe('<p>2 * 3 * 4 and snake_case_name</p>')
    expect(html('')).toBe('')
    expect(html('  \n\n ')).toBe('')
  })

  describe('a link alone on its line', () => {
    const big = 'inline-flex min-h-12 min-w-11 items-center underline'
    const aloneCount = (out: string) => out.split(`class="${big}"`).length - 1

    test('gets a large tap target in a list item, a heading, and a paragraph', () => {
      expect(aloneCount(html('- [a](https://a.example)\n- [Shop](https://b.example)'))).toBe(2)
      expect(aloneCount(html('1. [Shop](https://b.example)'))).toBe(1)
      expect(aloneCount(html('# [Shop](https://b.example)'))).toBe(1)
      expect(aloneCount(html('[Shop](https://b.example)'))).toBe(1)
    })

    test('still counts with bold or italic around it', () => {
      for (const mark of ['**', '__', '*', '_']) {
        expect(aloneCount(html(`- ${mark}[Shop](https://b.example)${mark}`))).toBe(1)
      }
    })

    test('does not count when a marker has a space inside it, so it is not emphasis', () => {
      expect(aloneCount(html('- _ [Shop](https://b.example) _'))).toBe(0)
      expect(aloneCount(html('_[S](https://x.example) _'))).toBe(0)
      expect(aloneCount(html('*[S](https://x.example) *'))).toBe(0)
    })

    test('counts for one line of a two-line paragraph, with no blank line between', () => {
      const out = html('Sizes:\n[Shop](https://b.example)')
      expect(aloneCount(out)).toBe(1)
      expect(out).toMatch(/^<p>Sizes:<br\/><a [^>]*>Shop<\/a><\/p>$/)
      expect(aloneCount(html('[Shop](https://b.example)\nmore words'))).toBe(1)
    })

    test('does not apply to a link in a sentence', () => {
      const out = html('Try [Shop](https://b.example) for sizes.')
      expect(aloneCount(out)).toBe(0)
      expect(out).toMatch(/<a [^>]*class="underline"/)
      expect(aloneCount(html('- see [Shop](https://b.example)'))).toBe(0)
    })

    test('does not apply to a line with two links', () => {
      const out = html('[A](https://a.example) [B](https://b.example)')
      expect(aloneCount(out)).toBe(0)
      expect(out.split('class="underline"').length - 1).toBe(2)
      expect(aloneCount(html('[A](https://a.example) and [B](https://b.example)'))).toBe(0)
    })

    test('does not apply to a link with an unsafe scheme', () => {
      const out = html('- [click](javascript:alert(1))')
      expect(out).not.toContain('<a')
      expect(out).toContain('[click](javascript:alert(1))')
    })
  })
})
