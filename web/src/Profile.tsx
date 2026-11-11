import { useId, useState, type FormEvent } from 'react'
import { getMe, removeAvatar, setAvatar, updateUser, type PartialDate, type User, type UserChanges, type Written } from './api'
import { UserAvatar } from './Avatar'
import { BirthdayFields, toPartialDate, type BirthdayText } from './BirthdayFields'
import { NameFields } from './NameFields'
import { OccasionsPart } from './Occasions'
import { usePhotoPicker } from './PhotoPicker'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { button, field, primary } from './ui'

function birthdayText(d: PartialDate): BirthdayText {
  return { month: String(d.month), day: String(d.day), year: d.year === null ? '' : String(d.year) }
}

function sameDate(a: PartialDate, b: PartialDate): boolean {
  return a.month === b.month && a.day === b.day && a.year === b.year
}

/**
 * A user's profile screen, for the user and for anyone who acts for them
 * (docs/spec.md, Users and parents, Screens): the name, last name, avatar, birthday and
 * good to know, and no timezone field. The photo is saved on its own, as soon as it is chosen
 * or removed, and the user's occasions are below the form (see `OccasionsPart`).
 * Saving sends only the
 * fields that changed, as a merge patch. It never confirms the profile, so an
 * edit by someone else leaves it as it was.
 * A caller who cannot edit the user sees the missing-page message.
 */
export function Profile({ userId, me, onMeChange }: { userId: string; me: User; onMeChange: (me: User) => void }) {
  return (
    <Resource<User> path={`/api/v2/users/${userId}`}>
      {(user) => (user.can_edit ? <ProfileForm initial={user} isMe={user.id === me.id} onMeChange={onMeChange} /> : <NotFound />)}
    </Resource>
  )
}

function ProfileForm({ initial, isMe, onMeChange }: { initial: User; isMe: boolean; onMeChange: (me: User) => void }) {
  // The user as the server has them: what the form is compared with.
  const [saved, setSaved] = useState(initial)
  const [name, setName] = useState(initial.name)
  const [last, setLast] = useState(initial.last_name ?? '')
  const [birthday, setBirthday] = useState(birthdayText(initial.birthday))
  const [note, setNote] = useState(initial.good_to_know ?? '')
  const [done, setDone] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const hintId = useId()

  async function save(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setDone(false)
    const first = name.trim()
    const date = toPartialDate(birthday)
    if (!first) {
      setError(isMe ? 'Enter your first name.' : 'Enter a first name.')
      return
    }
    if (!date) {
      setError(isMe ? 'Choose your birthday month and day.' : 'Choose a birthday month and day.')
      return
    }

    const changes: UserChanges = {}
    if (first !== saved.name) changes.name = first
    if (last.trim() !== (saved.last_name ?? '')) changes.last_name = last.trim() || null
    if (!sameDate(date, saved.birthday)) changes.birthday = date
    if (note.trim() !== (saved.good_to_know ?? '')) changes.good_to_know = note.trim() || null

    if (Object.keys(changes).length === 0) {
      setDone(true)
      return
    }
    setBusy(true)
    try {
      const res = await updateUser(saved.id, changes)
      if (!res.ok) {
        setError(res.detail)
      } else {
        setSaved(res.value)
        setName(res.value.name)
        setLast(res.value.last_name ?? '')
        setNote(res.value.good_to_know ?? '')
        setDone(true)
        // Menu shows the user's own name, so the app asks for it again.
        if (isMe) void getMe().then((m) => m && onMeChange(m), () => {})
      }
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <Screen title={isMe ? 'Your profile' : `${saved.name}'s profile`}>
      <Photo
        user={saved}
        onChange={(user) => {
          setSaved((current) => ({ ...current, avatar: user.avatar }))
          if (isMe) onMeChange(user)
        }}
      />
      <form onSubmit={save} className="flex max-w-xl flex-col gap-5" noValidate>
        <NameFields first={name} last={last} onFirst={setName} onLast={setLast} own={isMe} />
        <BirthdayFields value={birthday} onChange={setBirthday} />
        <div className="flex flex-col gap-1.5">
          <label className="flex flex-col gap-1.5">
            <span className="font-semibold">Good to know</span>
            <textarea
              className={`${field} min-h-32`}
              value={note}
              maxLength={2000}
              aria-describedby={hintId}
              onChange={(e) => {
                setNote(e.target.value)
                setDone(false)
              }}
            />
          </label>
          <p id={hintId} className="text-mute">
            Everyone who can see {isMe ? 'your' : 'their'} list can read this.
          </p>
        </div>
        {error && <p role="alert">{error}</p>}
        {done && <output>Saved.</output>}
        <button type="submit" className={primary} disabled={busy}>
          Save
        </button>
      </form>
      <OccasionsPart user={saved} />
    </Screen>
  )
}

const quietButton = 'flex min-h-12 items-center justify-center font-semibold underline disabled:text-mute'

/**
 * The user's photo, or their initials, with the buttons that add, change, and
 * remove it. A photo is saved at once: it uploads, and then the user is changed
 * to use it. The server deletes a photo that is removed or replaced.
 */
function Photo({ user, onChange }: { user: User; onChange: (user: User) => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const label = user.avatar ? 'Change photo' : 'Add a photo'

  async function run(change: () => Promise<Written<User>>) {
    setBusy(true)
    setError(null)
    try {
      const res = await change()
      if (res.ok) onChange(res.value)
      else setError(res.detail)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  const picker = usePhotoPicker(label, (file) => void run(() => setAvatar(user.id, file)))

  return (
    <section aria-label="Photo" className="flex max-w-xl flex-col gap-3">
      <div className="flex items-center gap-4">
        <UserAvatar user={user} size={88} />
        <div className="flex flex-1 flex-col">
          {picker.input}
          <button type="button" className={button} disabled={busy} onClick={picker.open}>
            {label}
          </button>
          {user.avatar && (
            <button type="button" className={quietButton} disabled={busy} onClick={() => void run(() => removeAvatar(user.id))}>
              Remove photo
            </button>
          )}
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
    </section>
  )
}
