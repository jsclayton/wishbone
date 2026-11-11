import { useEffect, useState, type ReactNode } from 'react'
import { edges } from './Screen'

/** The white button on the band. It is the same in light and dark. */
export const bandAction =
  'flex min-h-15.5 w-full items-center justify-center rounded-md bg-on-band px-6 py-3 text-xl font-extrabold text-band'

/** The whole page is the band, with the content at the bottom. Sign-in pages have no top row. */
export function SignInFrame({ children }: { children?: ReactNode }) {
  return (
    <div className="flex flex-1 flex-col bg-band text-on-band">
      <div className={`${edges} flex flex-1 flex-col justify-end pt-[max(1rem,env(safe-area-inset-top))] pb-[max(2.5rem,env(safe-area-inset-bottom))]`}>
        <div className="flex w-full max-w-xl flex-col gap-5">{children}</div>
      </div>
    </div>
  )
}

/** Wishbone as the page's heading. */
export function BrandHeading() {
  return <h1 className="text-[3.5rem]/none font-extrabold tracking-[-0.03em]">Wishbone</h1>
}

/** A page with something to say: a heading, a sentence, and its actions. */
export function SignInMessage({ heading, body, children }: { heading: string; body: string; children: ReactNode }) {
  return (
    <SignInFrame>
      <div className="flex flex-col gap-2">
        <span className="font-extrabold">Wishbone</span>
        <h1 className="text-[2.25rem]/[1.1] font-extrabold tracking-[-0.02em] min-[390px]:text-[2.75rem]/[1.05]">{heading}</h1>
        <p className="text-xl">{body}</p>
      </div>
      <div className="flex flex-col gap-2">{children}</div>
    </SignInFrame>
  )
}

export function TooManyTries({ onTryAgain }: { onTryAgain: () => void }) {
  return (
    <SignInMessage heading="Too many tries" body="Wait a minute, then try again.">
      <button type="button" className={bandAction} onClick={onTryAgain}>
        Try again
      </button>
    </SignInMessage>
  )
}

export type SignInStep = 'link-problem' | 'passkey-failed' | 'too-many'

function currentStep() {
  return (window.history.state as { signInStep?: SignInStep } | null)?.signInStep ?? null
}

/**
 * A new page replaces the button that held focus. Focus on `main`, as a move
 * between screens does, makes a screen reader announce the new page.
 */
export function focusMain() {
  document.querySelector('main')?.focus({ preventScroll: true })
}

/**
 * A problem that comes after a tap is its own history entry at the same address,
 * so Back leaves it. These pages have no address of their own: the route table
 * is for signed-in screens, and a problem can't be opened from a link.
 */
export function useSignInStep() {
  const [step, setStep] = useState(currentStep)

  useEffect(() => {
    const sync = () => {
      setStep(currentStep())
      focusMain()
    }
    window.addEventListener('popstate', sync)
    return () => window.removeEventListener('popstate', sync)
  }, [])

  function show(next: SignInStep) {
    window.history.pushState({ inApp: true, signInStep: next }, '')
    setStep(next)
    focusMain()
  }

  return [step, show] as const
}
