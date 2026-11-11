import { useState } from 'react'
import { Screen } from './Screen'
import { primary } from './ui'

/** `button`, at the size and weight of the action above it (`primary`). */
const keep = 'flex min-h-15.5 items-center justify-center rounded-md bg-tile px-6 py-3 text-xl font-extrabold text-ink'

/**
 * The screen before a destructive action. Give it its own line in the route
 * table, so it is a history entry and the browser's Back leaves it. The button
 * repeats the exact action. `onConfirm` does the work and moves on; if it
 * throws, the member stays here and can try again.
 */
export function Confirm({
  heading,
  statement,
  details = [],
  action,
  onConfirm,
  keepHref,
  keepLabel = 'Keep it',
}: {
  heading: string
  statement: string
  details?: string[]
  action: string
  onConfirm: () => Promise<void>
  keepHref: string
  keepLabel?: string
}) {
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)

  async function confirm() {
    setBusy(true)
    setFailed(false)
    try {
      await onConfirm()
    } catch {
      setFailed(true)
    }
    setBusy(false)
  }

  return (
    <Screen title={heading}>
      <p className="text-xl font-semibold">{statement}</p>
      {details.length > 0 && (
        <div className="flex flex-col">
          {details.map((line) => (
            <p key={line} className="border-t border-hair py-3">
              {line}
            </p>
          ))}
        </div>
      )}
      <div className="mt-auto flex flex-col gap-2 sm:mt-4 sm:max-w-md">
        {failed && <p role="alert">Something went wrong. Try again.</p>}
        <button type="button" className={primary} onClick={confirm} disabled={busy}>
          {action}
        </button>
        <a href={keepHref} className={keep}>
          {keepLabel}
        </a>
      </div>
    </Screen>
  )
}
