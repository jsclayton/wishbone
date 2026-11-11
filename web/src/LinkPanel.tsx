import { useEffect, useRef, useState } from 'react'
import makeQr from 'qrcode-generator'
import type { LoginLinkMade } from './api'
import { button, field } from './ui'

function qrSvg(text: string): string {
  const qr = makeQr(0, 'M')
  qr.addData(text)
  qr.make()
  return qr.createSvgTag({ scalable: true, margin: 2 })
}

/** How long a link has left, in words: "15 minutes", "7 days". */
function timeLeft(expiresAt: string): string {
  const minutes = Math.max(1, Math.round((new Date(expiresAt).getTime() - Date.now()) / 60_000))
  const plural = (n: number, unit: string) => `${n} ${unit}${n === 1 ? '' : 's'}`
  if (minutes <= 90) return plural(minutes, 'minute')
  if (minutes <= 48 * 60) return plural(Math.round(minutes / 60), 'hour')
  return plural(Math.round(minutes / (24 * 60)), 'day')
}

/**
 * A login link as a QR code, a prompt, how long the link lasts, and one button
 * that copies the link (and Share where the browser has it). The QR code always
 * comes before the link. The link itself shows only once the user has tried to
 * copy it, so the QR code stays the first choice. The prompt and the label of
 * the copy button are the caller's: `prompt` has the default "Scan this with your
 * other device." and `copyLabel` has the default "Copy link".
 */
export function LinkPanel({
  link,
  label,
  qrAlt,
  prompt = 'Scan this with your other device.',
  copyLabel = 'Copy link',
}: {
  link: LoginLinkMade
  label: string
  qrAlt: string
  prompt?: string
  copyLabel?: string
}) {
  const [copied, setCopied] = useState(false)
  const [copyFailed, setCopyFailed] = useState(false)
  const [linkShown, setLinkShown] = useState(false)
  const linkInput = useRef<HTMLInputElement>(null)
  // Read once, so the words don't change while the panel is open.
  const [left] = useState(() => timeLeft(link.expires_at))

  // No secure context, or permission denied: select the link so the user can copy it.
  useEffect(() => {
    if (copyFailed) linkInput.current?.select()
  }, [copyFailed])

  async function copy() {
    setCopyFailed(false)
    setLinkShown(true)
    try {
      await navigator.clipboard.writeText(link.url)
      setCopied(true)
    } catch {
      setCopyFailed(true)
    }
  }

  const copyButton = (
    <button type="button" className={button} onClick={copy}>
      {copied ? 'Copied' : copyLabel}
    </button>
  )
  const shareButton = typeof navigator.share === 'function' && (
    <button
      type="button"
      className={button}
      onClick={() => navigator.share({ title: 'Wishbone', url: link.url }).catch(() => undefined)}
    >
      Share
    </button>
  )

  return (
    <section aria-label={label} className="flex flex-col gap-4">
      <img
        alt={qrAlt}
        src={`data:image/svg+xml;utf8,${encodeURIComponent(qrSvg(link.url))}`}
        className="mx-auto w-60 max-w-full bg-white p-2"
      />
      <p className="text-xl font-extrabold">{prompt}</p>
      <p className="font-mono text-mute">Works once. Expires in {left}.</p>
      {linkShown && <input ref={linkInput} className={field} readOnly value={link.url} aria-label="Login link" />}
      {copyFailed && <p role="alert">Copy failed. Select the link and copy it.</p>}
      <div className="flex flex-wrap gap-3">
        {copyButton}
        {shareButton}
      </div>
    </section>
  )
}
