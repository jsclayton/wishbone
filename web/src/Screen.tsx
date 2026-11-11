import type { ReactNode } from 'react'
import { goBack } from './navigation'
import { primary } from './ui'
import { isInstalledApp } from './updates'

/** The left and right edge of a screen. The max is for a phone on its side, with the notch at one end. */
export const edges =
  'mx-auto w-full max-w-300 pr-[max(1rem,env(safe-area-inset-right))] pl-[max(1rem,env(safe-area-inset-left))] sm:pr-[max(1.5rem,env(safe-area-inset-right))] sm:pl-[max(1.5rem,env(safe-area-inset-left))]'

/** The Back button of the installed app, which has no browser controls. */
function BackButton() {
  return (
    <button type="button" className="flex h-11 min-w-11 items-center font-semibold underline" onClick={() => goBack('/')}>
      Back
    </button>
  )
}

/** Wishbone on the left and Menu on the right. Pads for the iPhone notch: the page sets `viewport-fit=cover`. */
export function TopRow({ menu }: { menu: boolean }) {
  const showBack = isInstalledApp() && window.location.pathname !== '/'
  return (
    <nav aria-label="Top" className="flex items-center justify-between gap-4 pt-[max(0.5rem,env(safe-area-inset-top))]">
      <div className="flex items-center gap-4">
        {showBack && <BackButton />}
        <a href="/" className="flex h-11 items-center font-extrabold">
          Wishbone
        </a>
      </div>
      {menu && (
        <a href="/menu" className="flex h-11 min-w-11 items-center font-semibold underline">
          Menu
        </a>
      )}
    </nav>
  )
}

/**
 * The frame of a signed-in screen: the band, full width, with the top row and
 * the heading, then the content. Content is centered with a 1200px maximum.
 * Menu leaves out the link to itself. A `badge` sits inline after the title, in
 * the heading ("Deactivated" on a user's page). An `action` sits at the right end of
 * the heading's row. It moves under the heading when the heading's longest
 * word and the action don't fit on one line. A `band` sits under the heading,
 * inside the band, for a screen that has something to say there (Home's next
 * occasion).
 */
export function Screen({
  title,
  menu = true,
  badge,
  action,
  band,
  children,
}: {
  title: string
  menu?: boolean
  badge?: ReactNode
  action?: ReactNode
  band?: ReactNode
  children: ReactNode
}) {
  const heading = 'text-[2.5rem]/[1.1] font-extrabold tracking-[-0.02em]'
  return (
    <>
      <header className="bg-band text-on-band">
        <div className={`${edges} flex flex-col pb-5`}>
          <TopRow menu={menu} />
          {action !== undefined ? (
            <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
              <h1 className={`${heading} max-w-full grow basis-0 wrap-break-word`}>
                {title}
                {badge}
              </h1>
              <div className="shrink-0">{action}</div>
            </div>
          ) : (
            <h1 className={heading}>
              {title}
              {badge}
            </h1>
          )}
          {band}
        </div>
      </header>
      <div className={`${edges} flex flex-1 flex-col gap-4 pt-6 pb-[max(1.5rem,env(safe-area-inset-bottom))]`}>{children}</div>
    </>
  )
}

function Problem({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Screen title={title}>
      <div className="flex max-w-xl flex-col gap-5.5">{children}</div>
    </Screen>
  )
}

/** One message for a missing page and a page not shared with the member, so it doesn't reveal which is which. */
export function NotFound() {
  return (
    <Problem title="We can't find that page">
      <p className="text-xl">It may have been removed, or it may not be shared with you.</p>
      <a href="/" className={`${primary} flex items-center justify-center`}>
        Go to Home
      </a>
    </Problem>
  )
}

/** The server didn't answer who is signed in. */
export function CantReach({ onRetry, retrying }: { onRetry: () => void; retrying: boolean }) {
  return (
    <Problem title="Can't reach Wishbone">
      <p className="text-xl">Check your internet connection and try again. If that doesn't help, Wishbone may be down for a while.</p>
      <button type="button" className={primary} onClick={onRetry} disabled={retrying}>
        Try again
      </button>
    </Problem>
  )
}
