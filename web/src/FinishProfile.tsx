import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { confirmProfile, logOut, setAvatar, updateUser, type ImageSummary, type User, type UserChanges } from './api'
import { UserAvatar } from './Avatar'
import { BirthdayFields, toPartialDate, type BirthdayText } from './BirthdayFields'
import { NameFields } from './NameFields'
import { usePhotoPicker } from './PhotoPicker'
import { edges } from './Screen'
import { focusMain } from './SignInFrame'
import { button, link, primary } from './ui'

function birthdayText({ month, day, year }: User['birthday']): BirthdayText {
  return { month: String(month), day: String(day), year: year === null ? '' : String(year) }
}

/** A quiet underlined link at the bottom of a step. */
const quietLink = `${link} w-full justify-center font-semibold`

/** The frame of a step: the band with its heading, and no top row, so nobody wanders off before the profile is done. */
function StepFrame({ title, children }: { title: string; children: ReactNode }) {
  return (
    <>
      <header className="bg-band text-on-band">
        <div className={`${edges} pt-[max(1.5rem,env(safe-area-inset-top))] pb-5`}>
          <h1 className="text-[2.25rem]/[1.1] font-extrabold tracking-[-0.02em]">{title}</h1>
        </div>
      </header>
      <div className={`${edges} flex flex-1 flex-col pt-6 pb-[max(1.5rem,env(safe-area-inset-bottom))]`}>{children}</div>
    </>
  )
}

type Step = 'profile' | 'photo'

function currentStep(): Step {
  return (window.history.state as { firstStep?: Step } | null)?.firstStep ?? 'profile'
}

/**
 * The steps are one screen each, and each is its own history entry, so Back
 * goes to the step before. They have no top row, so the browser's
 * Back is the only way back. Back and Forward come through popstate. App
 * listens for it too, and only reads the path.
 */
function useStep() {
  const [step, setStep] = useState(currentStep)

  useEffect(() => {
    const sync = () => {
      setStep(currentStep())
      focusMain()
    }
    window.addEventListener('popstate', sync)
    return () => window.removeEventListener('popstate', sync)
  }, [])

  function go(next: Step) {
    window.history.pushState({ inApp: true, firstStep: next }, '')
    setStep(next)
    focusMain()
    window.scrollTo(0, 0)
  }

  return [step, go] as const
}

type Names = { name: string; last_name: string | null }
/** The profile fields as the server holds them. */
type Held = Names & { birthday: User['birthday'] }

/**
 * The steps that come before everything else at sign-in, until the user has
 * confirmed their profile (docs/spec.md, Admin and setup, Screens). The first
 * confirms the names and the birthday: the admin who made the user may have
 * entered some of it, so the form starts with what is there. The second offers
 * a photo, and can be skipped. The profile is confirmed when the second step is
 * done, so a user who leaves between the two sees both again. When the steps are
 * done, the history entry forgets its step, so a later sign-in in this tab
 * starts at the first. The passkey comes with the passkeys. There is no top row,
 * but Log out stays on the first step, so nobody is stuck.
 */
export function FinishProfile({ me, onDone, onLoggedOut }: { me: User; onDone: () => void; onLoggedOut: () => void }) {
  const [step, go] = useStep()
  // What the first step shows, and what the server holds of it: Back from the
  // second step finds the form as it was left, and a second Continue sends only
  // what changed since the last one.
  const [name, setName] = useState(me.name)
  const [last, setLast] = useState(me.last_name ?? '')
  const [birthday, setBirthday] = useState(birthdayText(me.birthday))
  const [held, setHeld] = useState<Held>({ name: me.name, last_name: me.last_name, birthday: me.birthday })
  const [avatar, setAvatarImage] = useState<ImageSummary | null>(me.avatar)

  function finish() {
    // The entry stays where it is, with its path, query, and hash. It forgets the
    // step but keeps the mark that an in-app entry comes before it, so Back works.
    window.history.replaceState({ inApp: true }, '')
    onDone()
  }

  if (step === 'photo') {
    return <PhotoStep me={me} shown={held} avatar={avatar} onAvatar={setAvatarImage} onDone={finish} />
  }
  return (
    <ProfileStep
      me={me}
      held={held}
      values={{ name, last, birthday }}
      onChange={{ name: setName, last: setLast, birthday: setBirthday }}
      onSaved={(saved) => {
        setHeld(saved)
        go('photo')
      }}
      onLoggedOut={onLoggedOut}
    />
  )
}

function ProfileStep({
  me,
  held,
  values,
  onChange,
  onSaved,
  onLoggedOut,
}: {
  me: User
  held: Held
  values: { name: string; last: string; birthday: BirthdayText }
  onChange: { name: (v: string) => void; last: (v: string) => void; birthday: (v: BirthdayText) => void }
  onSaved: (saved: Held) => void
  onLoggedOut: () => void
}) {
  const { name, last, birthday } = values
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    const first = name.trim()
    const date = toPartialDate(birthday)
    if (!first || !date) {
      setError('Enter your first name, birthday month, and birthday day.')
      return
    }
    const changes: UserChanges = {}
    if (first !== held.name) changes.name = first
    if (last.trim() !== (held.last_name ?? '')) changes.last_name = last.trim() || null
    if (date.month !== held.birthday.month || date.day !== held.birthday.day || date.year !== held.birthday.year) changes.birthday = date

    setBusy(true)
    try {
      if (Object.keys(changes).length > 0) {
        const res = await updateUser(me.id, changes)
        if (!res.ok) {
          setError(res.detail)
          setBusy(false)
          return
        }
      }
      onSaved({ name: first, last_name: last.trim() || null, birthday: date })
      return
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  async function leave() {
    setBusy(true)
    try {
      await logOut()
      onLoggedOut()
    } catch {
      setError('Something went wrong. Try again.')
      setBusy(false)
    }
  }

  return (
    <StepFrame title="Finish your profile">
      <form onSubmit={submit} className="flex w-full max-w-xl flex-1 flex-col gap-4" noValidate>
        <NameFields first={name} last={last} onFirst={onChange.name} onLast={onChange.last} own />
        <div className="flex flex-col gap-2">
          <BirthdayFields value={birthday} onChange={onChange.birthday} />
          <p className="text-mute">With a year, people see the age you're turning.</p>
        </div>
        {error && <p role="alert">{error}</p>}
        <button type="submit" className={`${primary} mt-auto`} disabled={busy}>
          Continue
        </button>
        <button type="button" className={quietLink} onClick={leave} disabled={busy}>
          Log out
        </button>
      </form>
    </StepFrame>
  )
}

/**
 * Add a photo: the user chooses a file, which uploads and becomes their avatar
 * at once. Not now skips the step, and so does Use this photo once there is one.
 * Either one confirms the profile and lets the app go on.
 */
function PhotoStep({
  me,
  shown,
  avatar,
  onAvatar,
  onDone,
}: {
  me: User
  shown: Names
  avatar: ImageSummary | null
  onAvatar: (avatar: ImageSummary | null) => void
  onDone: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function upload(file: File) {
    setBusy(true)
    setError(null)
    try {
      const res = await setAvatar(me.id, file)
      if (res.ok) onAvatar(res.value.avatar)
      else setError(res.detail)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  async function next() {
    setBusy(true)
    setError(null)
    try {
      await confirmProfile(me.id)
      onDone()
      return
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  const picker = usePhotoPicker(undefined, (file) => void upload(file))

  return (
    <StepFrame title="Add a photo">
      <div className="flex flex-col items-center gap-3.5 pt-4 text-center">
        <UserAvatar user={{ ...shown, avatar }} size={180} shrinkOnShortScreen />
        <p className="pt-2 text-[1.625rem]/[1.2] font-extrabold">Help people recognize you</p>
        <p className="text-xl">Your photo shows next to your name.</p>
      </div>
      {picker.input}
      {error && <p role="alert">{error}</p>}
      <div className="mt-auto flex flex-col gap-2">
        {avatar ? (
          <>
            <button type="button" className={primary} disabled={busy} onClick={next}>
              Use this photo
            </button>
            <button type="button" className={button} disabled={busy} onClick={picker.open}>
              Choose a different photo
            </button>
          </>
        ) : (
          <>
            <button type="button" className={primary} disabled={busy} onClick={picker.open}>
              Choose a photo
            </button>
            <button type="button" className={quietLink} disabled={busy} onClick={next}>
              Not now
            </button>
          </>
        )}
        <p className="text-center text-mute">You can change it later in Your profile.</p>
      </div>
    </StepFrame>
  )
}
