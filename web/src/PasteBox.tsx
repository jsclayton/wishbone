import { useRef, useState, type FormEvent } from 'react'
import { PHOTO_TYPES, search, type SearchResult, type User } from './api'
import { looksLikeLink, openAddForm, photoDay, readPhoto, unread, type Draft } from './addDraft'
import { notice } from './Picker'
import { button, field } from './ui'

/** What the user sees for a photo the browser can't read. */
const PHOTO_UNREADABLE = "We couldn't read that photo. Try another one."

/**
 * "Add to my list": paste a link, type what you want, or take a photo. All three
 * go to the search (`POST /search`), and the add form of the user's primary list
 * opens with what it found. Nothing is saved until the user adds the item
 * (docs/spec.md, Search, Screens). The camera button sits inside the field.
 * It opens the device's choice of camera or photo library, and the photo stays
 * in the browser until the item is saved.
 */
export function PasteBox({ me }: { me: User }) {
  const [input, setInput] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [reading, setReading] = useState<{ link: string | null; photo: Draft['photo'] } | null>(null)
  const request = useRef<AbortController | null>(null)
  const picker = useRef<HTMLInputElement>(null)
  const list = me.primary_list

  if (!list) return null
  const listId = list.id
  const text = input.trim()

  function open(kind: Draft['kind'], result: SearchResult, limited: boolean, photo: Draft['photo']) {
    openAddForm(listId, { kind, result, limited, photo, day: photoDay() })
  }

  /** Sends the input to the search. A refusal that the user can fix stays here, with their input. */
  async function run(query: { text: string } | { image: File }, photo: Draft['photo']) {
    const kind: Draft['kind'] = photo ? 'photo' : looksLikeLink((query as { text: string }).text) ? 'link' : 'words'
    const typed = 'text' in query ? query.text : null
    const controller = new AbortController()
    request.current = controller
    setReading({ link: kind === 'link' ? typed : null, photo })
    setError(null)
    try {
      const res = await search(query, controller.signal)
      if (controller.signal.aborted) return
      if (res.ok) {
        open(kind, res.result, false, photo)
        return
      }
      if (res.reason === 'limited') {
        open(kind, unread(typed), true, photo)
        return
      }
      setError(res.detail)
    } catch {
      // "Fill it in myself" ended the wait, and the form is already open.
      if (controller.signal.aborted) return
      setError('Something went wrong. Try again.')
    }
    setReading(null)
  }

  function start(e: FormEvent) {
    e.preventDefault()
    if (text === '' || reading) return
    void run({ text }, null)
  }

  async function choose(file: File) {
    if (reading) return
    setError(null)
    let photo: Draft['photo']
    try {
      photo = await readPhoto(file)
    } catch {
      setError(PHOTO_UNREADABLE)
      return
    }
    void run({ image: file }, photo)
  }

  function fillItInMyself() {
    request.current?.abort()
    if (!reading) return
    const kind: Draft['kind'] = reading.photo ? 'photo' : reading.link ? 'link' : 'words'
    open(kind, unread(reading.photo ? null : text), false, reading.photo)
  }

  return (
    <form onSubmit={start} aria-label="Add to my list" noValidate className="flex w-full max-w-xl flex-col gap-3">
      <div className="flex flex-col gap-1">
        <label htmlFor="paste">Add to my list</label>
        <div className="relative">
          <input
            id="paste"
            className={`${field} pr-16`}
            value={input}
            readOnly={reading !== null}
            aria-describedby={text === '' && !error ? 'paste-hint' : undefined}
            onChange={(e) => {
              setInput(e.target.value)
              setError(null)
            }}
          />
          <button
            type="button"
            aria-label="Take or choose a photo"
            disabled={reading !== null}
            className="absolute top-0.5 right-0.5 flex size-13.5 items-center justify-center border-l border-hair text-ink"
            onClick={() => picker.current?.click()}
          >
            <svg width="30" height="30" viewBox="0 0 32 32" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinejoin="round" aria-hidden="true">
              <path d="M4 11 H10 L12.5 7 H19.5 L22 11 H28 V26 H4 Z" />
              <circle cx="16" cy="18" r="5" />
            </svg>
          </button>
          {/* No `capture`, so a phone offers its camera and its library. */}
          <input
            ref={picker}
            type="file"
            hidden
            accept={PHOTO_TYPES}
            aria-label="Photo file"
            onChange={(e) => {
              const picked = e.currentTarget
              const file = picked.files?.[0]
              if (!file) return
              void choose(file).finally(() => {
                picked.value = '' // so choosing the same file again still fires
              })
            }}
          />
        </div>
      </div>
      {text === '' && !error && !reading && (
        <p id="paste-hint" className="text-mute">
          Paste a link, type what you want, or take a photo.
        </p>
      )}
      {reading && (
        <>
          <output>{reading.link ? 'Looking for the name, price and picture. This usually takes a few seconds.' : 'Looking at what you gave us.'}</output>
          <button type="button" className={button} onClick={fillItInMyself}>
            Fill it in myself
          </button>
        </>
      )}
      {error && (
        <p role="alert" className={notice}>
          {error}
        </p>
      )}
      {!reading && (
        <button type="submit" className={button}>
          Add
        </button>
      )}
    </form>
  )
}
