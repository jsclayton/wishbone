import { useState } from 'react'
import { getMe, RateLimitedError } from './api'
import { signInWithPasskey } from './passkeyBrowser'
import { Screen } from './Screen'
import { bandAction, SignInMessage, TooManyTries, useSignInStep } from './SignInFrame'
import { primary } from './ui'

/**
 * What a browser with no session sees once the instance has users. It shows no
 * user names or household details, and so does every message here. A person
 * signs in with a login link, or with a passkey.
 */
export function SignedOut({ onSignedIn }: { onSignedIn: () => void }) {
  const [step, showStep] = useSignInStep()
  const [busy, setBusy] = useState(false)

  async function signIn() {
    setBusy(true)
    try {
      await signInWithPasskey()
    } catch (e) {
      showStep(e instanceof RateLimitedError ? 'too-many' : 'passkey-failed')
      setBusy(false)
      return
    }
    // A browser that refuses the session cookie signs in and still gets a 401
    // from /users/me. Say so, instead of leaving the button dead. If it gives no
    // answer at all, App says it can't reach Wishbone.
    const me = await getMe().catch(() => undefined)
    if (me === null) {
      showStep('passkey-failed')
      setBusy(false)
      return
    }
    onSignedIn()
  }

  const back = () => window.history.back()

  if (step === 'too-many') return <TooManyTries onTryAgain={back} />
  if (step === 'passkey-failed') {
    return (
      <SignInMessage heading="That didn't work" body="Your passkey wasn't accepted. Try again, or ask someone for a new link.">
        <button type="button" className={bandAction} onClick={back}>
          Try again
        </button>
      </SignInMessage>
    )
  }
  return (
    <Screen title="Welcome to Wishbone" menu={false}>
      <p className="max-w-xl text-xl">Open your login link to sign in. If you don't have one, ask the person who runs Wishbone.</p>
      <div className="flex max-w-xl flex-col gap-2">
        <button type="button" className={primary} onClick={signIn} disabled={busy}>
          Sign in with passkey
        </button>
      </div>
    </Screen>
  )
}
