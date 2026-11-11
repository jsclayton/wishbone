import { dismissUpdateBanner, useUpdateBanner } from './updates'
import { button } from './ui'

/** Tells a browser tab that a new version is ready. An installed app reloads on its own. */
export function UpdateNotifier({ reload = () => window.location.reload() }: { reload?: () => void }) {
  const visible = useUpdateBanner()
  if (!visible) return null
  return (
    <output className="fixed inset-x-0 bottom-0 z-10 border-t border-hair bg-bg p-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
      <span className="mx-auto flex max-w-md flex-col gap-3">
        <span>A new version is available</span>
        <span className="flex gap-3">
          <button type="button" className={button} onClick={reload}>Refresh</button>
          <button type="button" className={button} onClick={dismissUpdateBanner}>Dismiss</button>
        </span>
      </span>
    </output>
  )
}
