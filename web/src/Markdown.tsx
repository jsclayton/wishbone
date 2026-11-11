import { Fragment, type ReactNode } from 'react'
import { aloneLink } from './ui'

/**
 * A small, safe Markdown renderer for the "good to know" note.
 *
 * It builds React elements and never sets inner HTML, so React escapes every
 * character of the note: raw HTML shows up as text. It supports paragraphs,
 * line breaks, bullet and numbered lists, headings (shown as small headings),
 * bold, italic, inline code, and http(s) links. Anything else stays as typed.
 */
export function Markdown({ text }: { text: string }) {
  return <>{blocks(text)}</>
}

type Block =
  | { kind: 'p'; lines: string[] }
  | { kind: 'h'; text: string }
  | { kind: 'ul' | 'ol'; items: string[] }

const bullet = /^\s*[-*+]\s+(.*)$/
const numbered = /^\s*\d+[.)]\s+(.*)$/
const heading = /^\s*#{1,6}\s+(.*)$/

function parse(text: string): Block[] {
  const out: Block[] = []
  for (const line of text.replace(/\r\n?/g, '\n').split('\n')) {
    if (line.trim() === '') {
      out.push({ kind: 'p', lines: [] }) // a blank line ends the block
      continue
    }
    const last = out[out.length - 1]
    let m: RegExpExecArray | null
    if ((m = heading.exec(line))) {
      out.push({ kind: 'h', text: m[1]! })
      out.push({ kind: 'p', lines: [] })
    } else if ((m = bullet.exec(line))) {
      if (last?.kind === 'ul') last.items.push(m[1]!)
      else out.push({ kind: 'ul', items: [m[1]!] })
    } else if ((m = numbered.exec(line))) {
      if (last?.kind === 'ol') last.items.push(m[1]!)
      else out.push({ kind: 'ol', items: [m[1]!] })
    } else if (last?.kind === 'p') {
      last.lines.push(line.trim())
    } else {
      out.push({ kind: 'p', lines: [line.trim()] })
    }
  }
  return out.filter((b) => b.kind !== 'p' || b.lines.length > 0)
}

function blocks(text: string): ReactNode[] {
  return parse(text).map((b, i) => {
    switch (b.kind) {
      case 'h':
        return (
          <h3 key={i} className="font-semibold">
            {inlineLine(b.text)}
          </h3>
        )
      case 'ul':
      case 'ol': {
        const List = b.kind
        return (
          <List key={i} className={b.kind === 'ul' ? 'list-disc pl-6' : 'list-decimal pl-6'}>
            {b.items.map((it, j) => (
              <li key={j}>{inlineLine(it)}</li>
            ))}
          </List>
        )
      }
      case 'p':
        return (
          <p key={i}>
            {b.lines.map((l, j) => (
              <Fragment key={j}>
                {j > 0 && <br />}
                {inlineLine(l)}
              </Fragment>
            ))}
          </p>
        )
    }
  })
}

const webLink = /^\[[^\]]+\]\(https?:\/\/[^)\s]*\)$/i
// The italic entries use the same lookarounds as the emphasis patterns below.
const wrappers = [/^\*\*(.+)\*\*$/, /^__(.+)__$/, /^\*(?=\S)(.+)(?<=\S)\*$/, /^_(?=\S)(.+)(?<=\S)_$/]

/** Whether the line is one web link, perhaps in bold or italic, and nothing else. */
function isAlone(text: string): boolean {
  const t = text.trim()
  if (webLink.test(t)) return true
  for (const w of wrappers) {
    const m = w.exec(t)
    if (m) return isAlone(m[1]!)
  }
  return false
}

/** One line of a paragraph, a list item, or a heading. */
function inlineLine(text: string): ReactNode[] {
  return inline(text, isAlone(text))
}

// Earlier entries win when two start at the same place.
const patterns: { re: RegExp; render: (m: RegExpExecArray, key: number, alone: boolean) => ReactNode }[] = [
  { re: /`([^`]+)`/, render: (m, k) => <code key={k}>{m[1]}</code> },
  { re: /\*\*(.+?)\*\*/, render: (m, k, alone) => <strong key={k}>{inline(m[1]!, alone)}</strong> },
  { re: /(?<!\w)__(.+?)__(?!\w)/, render: (m, k, alone) => <strong key={k}>{inline(m[1]!, alone)}</strong> },
  { re: /\*(?=\S)(.+?)(?<=\S)\*/, render: (m, k, alone) => <em key={k}>{inline(m[1]!, alone)}</em> },
  { re: /(?<!\w)_(?=\S)(.+?)(?<=\S)_(?!\w)/, render: (m, k, alone) => <em key={k}>{inline(m[1]!, alone)}</em> },
  {
    re: /\[([^\]]+)\]\(([^)\s]+)\)/,
    render: (m, k, alone) =>
      /^https?:\/\//i.test(m[2]!) ? (
        <a key={k} href={m[2]} target="_blank" rel="noopener noreferrer nofollow" className={alone ? aloneLink : 'underline'}>
          {inline(m[1]!)}
        </a>
      ) : (
        // Not a web link: show what was typed, without a link.
        m[0]
      ),
  },
]

function inline(text: string, alone = false): ReactNode[] {
  const out: ReactNode[] = []
  let rest = text
  let key = 0
  while (rest) {
    let best: { m: RegExpExecArray; render: (typeof patterns)[number]['render'] } | undefined
    for (const p of patterns) {
      const m = p.re.exec(rest)
      if (m && (!best || m.index < best.m.index)) best = { m, render: p.render }
    }
    if (!best) break
    if (best.m.index > 0) out.push(rest.slice(0, best.m.index))
    out.push(best.render(best.m, key++, alone))
    rest = rest.slice(best.m.index + best.m[0].length)
  }
  if (rest) out.push(rest)
  return out
}
