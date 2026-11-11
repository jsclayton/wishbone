import { useState, type FormEvent } from 'react'
import { postSetup, type SetupProblem } from './api'
import { BirthdayFields, emptyBirthday, toPartialDate } from './BirthdayFields'
import { NameFields } from './NameFields'
import { Screen } from './Screen'
import { field, primary } from './ui'

const messages: Record<SetupProblem, string | null> = {
  code: "That setup code isn't right. Copy it from the server log.",
  rate_limited: 'Too many tries. Wait a minute, then try again.',
  // The server's own words say which birthday or name failed.
  invalid: null,
  // Somebody else finished setup. There is nothing to fix here.
  complete: null,
}

/**
 * What a new instance shows until its first user exists. The operator enters
 * the setup code from the server log, and the name and birthday of the first
 * user, who becomes the admin. `onDone` runs when setup is over, whether this
 * form finished it or somebody else did first.
 */
export function Setup({ onDone }: { onDone: () => void }) {
  const [code, setCode] = useState('')
  const [first, setFirst] = useState('')
  const [last, setLast] = useState('')
  const [birthday, setBirthday] = useState(emptyBirthday)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    const date = toPartialDate(birthday)
    if (!code.trim() || !first.trim() || !date) {
      setError('Enter the setup code, your first name, and your birthday month and day.')
      return
    }
    setBusy(true)
    try {
      const res = await postSetup({
        setup_code: code.trim(),
        name: first.trim(),
        ...(last.trim() ? { last_name: last.trim() } : {}),
        birthday: date,
      })
      if (res.ok || res.problem === 'complete') {
        onDone()
        return
      }
      setError(messages[res.problem] ?? res.detail)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <Screen title="Set up Wishbone" menu={false}>
      <form onSubmit={submit} className="flex max-w-xl flex-col gap-4" noValidate>
        <p className="text-xl">
          This Wishbone has no users yet. Enter the setup code from its log, then your name and birthday. You will be the admin.
        </p>
        <label className="flex flex-col gap-1.5">
          <span className="font-semibold">Setup code</span>
          <input
            className={`${field} font-mono`}
            value={code}
            onChange={(e) => setCode(e.target.value)}
            autoCapitalize="characters"
            autoComplete="off"
            autoCorrect="off"
            spellCheck={false}
          />
        </label>
        <NameFields first={first} last={last} onFirst={setFirst} onLast={setLast} own />
        <BirthdayFields value={birthday} onChange={setBirthday} />
        {error && <p role="alert">{error}</p>}
        <button type="submit" className={primary} disabled={busy}>
          Set up
        </button>
      </form>
    </Screen>
  )
}
