import { useRef, useState } from 'react'
import { addItem, downloadImage, updateItem, uploadImage, type List, type NewItem, type NewStoreLink, type User } from './api'
import { photoTitle, readDraft, type Draft } from './addDraft'
import { ItemForm } from './ItemForm'
import { ListChoice } from './ListChoice'
import { goBack, replaceTo } from './navigation'
import { notice } from './Picker'
import { Screen } from './Screen'
import { buttonLink, link as linkClass } from './ui'

const listPath = (id: string) => `/lists/${id}`

/** The longest notes the server takes, which a free-text search cuts to. */
const MAX_NOTES = 2000

/**
 * The add form of a list. Opened plain, it is blank. Opened by a search (the
 * paste box and its camera button), it starts with what the search found: the
 * title, the notes, the price, the store link, and the images to pick from
 * (docs/spec.md, Search). Nothing is saved until the user adds the item. Then
 * the app uploads the photo, or each picked image address, with `POST /images`
 * and adds the item with the images' IDs.
 */
export function AddForm({ list, me }: { list: List; me: User }) {
  // The draft belongs to the history entry this screen opened for.
  const [draft] = useState(readDraft)
  // The list the item goes to: the one the screen opened for, until the user picks another.
  const [target, setTarget] = useState(list.id)
  // An item that was added, but whose price the user removed could not be cleared.
  const [stranded, setStranded] = useState<{ id: string; title: string } | null>(null)
  const link = draft?.result.store_links[0]
  const [picked, setPicked] = useState(() => new Set(draft?.result.image_urls ?? []))
  // What was saved already, by where it came from. A refused add can be tried
  // again, and its images are not saved twice.
  const saved = useRef(new Map<string, string>())

  // The image the server makes primary is the first one saved: the photo, else the first picked address.
  const mainUrl = draft?.photo ? null : (draft?.result.image_urls.find((url) => picked.has(url)) ?? null)

  const prefill = draft
    ? {
        title: draft.result.title ?? (draft.photo ? photoTitle(draft.day) : ''),
        notes: draft.result.notes,
        price_minor: draft.result.price_minor ?? link?.price_minor ?? null,
        top_price_minor: draft.result.top_price_minor,
        currency: draft.result.currency ?? link?.currency ?? null,
      }
    : undefined

  /** The ID of an image the app saved, or the words to show when the save was refused. A download that fails is left out (`null`). */
  async function keep(source: string, save: () => ReturnType<typeof uploadImage>, leaveOut: boolean): Promise<string | { refused: string } | null> {
    const known = saved.current.get(source)
    if (known) return known
    try {
      const res = await save()
      if (res.ok) {
        saved.current.set(source, res.value.id)
        return res.value.id
      }
      return leaveOut ? null : { refused: res.detail }
    } catch (error) {
      if (leaveOut) return null
      throw error
    }
  }

  async function onSave(input: NewItem): Promise<string | null> {
    const body: NewItem = { ...input }
    const ids: string[] = []
    if (draft?.photo) {
      const photo = draft.photo.file
      const id = await keep('photo', () => uploadImage(photo), false)
      if (typeof id === 'object' && id) return id.refused
      if (id) ids.push(id)
    }
    // The image addresses are the shop's. One the server can't download is left out.
    for (const url of draft?.result.image_urls ?? []) {
      if (!picked.has(url)) continue
      const id = await keep(url, () => downloadImage(url), true)
      if (typeof id === 'string') ids.push(id)
    }
    if (ids.length > 0) {
      body.image_ids = ids
      body.primary_image_id = ids[0]
    }
    if (draft && draft.result.store_links.length > 0) {
      body.store_links = draft.result.store_links.map((l): NewStoreLink => {
        const out: NewStoreLink = { url: l.url }
        if (l.price_minor !== null) out.price_minor = l.price_minor
        if (l.currency !== null) out.currency = l.currency
        if (l.image_url !== null) out.image_url = l.image_url
        return out
      })
    }
    const res = await addItem(target, body)
    if (!res.ok) return res.detail
    // An item with no price takes the price of its first priced store link, and a
    // null price in the body doesn't say otherwise. When the user left the price
    // empty, clear what the server filled in (docs/routes.md, The item price).
    // The item is added either way. If the clear fails, the screen says so and
    // opens the item, because the list would show a price the user removed.
    if (body.price_minor == null && res.value.price_minor !== null) {
      let cleared = false
      try {
        cleared = (await updateItem(res.value.id, { price_minor: null })).ok
      } catch {
        // Same as a refusal.
      }
      if (!cleared) {
        setStranded({ id: res.value.id, title: res.value.title })
        return null
      }
    }
    // An item added to another list opens that list. Back then goes to where the user came from.
    if (draft || target !== list.id) replaceTo(listPath(target))
    else goBack(listPath(list.id))
    return null
  }

  function toggle(url: string) {
    setPicked((old) => {
      const next = new Set(old)
      if (!next.delete(url)) next.add(url)
      return next
    })
  }

  if (stranded) {
    return (
      <Screen title="Added">
        <p role="alert" className={notice}>
          {stranded.title} was added, but we couldn't remove its price. Open it to clear the price yourself.
        </p>
        <div className="flex max-w-md flex-col gap-2">
          <a href={`/items/${stranded.id}`} className={buttonLink}>
            Open {stranded.title}
          </a>
          <a href={listPath(list.id)} className={`${linkClass} justify-center font-semibold`}>
            Back to the list
          </a>
        </div>
      </Screen>
    )
  }

  return (
    <Screen title="Add an item">
      <ListChoice
        me={me}
        value={target}
        onChange={setTarget}
        fallback={
          <p className="text-lg text-mute">
            To{' '}
            <a href={listPath(list.id)} className="underline">
              {list.name}
            </a>
          </p>
        }
      />
      {draft && <Notices draft={draft} />}
      <ItemForm
        legend="Add an item"
        submit="Add"
        cancelHref={listPath(list.id)}
        prefill={prefill}
        options={draft?.result.options}
        duplicateOf={{ listId: target, urls: draft?.result.store_links.map((l) => l.url) ?? [] }}
        onSave={onSave}
      >
        {link && (
          <p className="flex flex-col gap-1">
            <span className="font-semibold">Store</span>
            <span>{link.store}</span>
          </p>
        )}
        {draft?.photo && (
          <img src={draft.photo.url} alt="What you picked" className="max-h-64 max-w-full self-start rounded-md object-contain" />
        )}
        {draft && draft.result.image_urls.length > 0 && (
          <fieldset className="flex flex-col gap-2">
            <legend className="font-semibold">Images</legend>
            <div className="flex flex-wrap gap-3">
              {draft.result.image_urls.map((url, i) => (
                <label key={url} className="flex flex-col items-center gap-1">
                  <img src={url} alt="" className="size-24 rounded-md bg-tile object-contain" />
                  <span className="flex min-h-12 items-center gap-2">
                    <input
                      type="checkbox"
                      className="size-6"
                      checked={picked.has(url)}
                      aria-label={`Use image ${i + 1}`}
                      onChange={() => toggle(url)}
                    />
                    {url === mainUrl ? 'Primary image' : ''}
                  </span>
                </label>
              ))}
            </div>
          </fieldset>
        )}
      </ItemForm>
    </Screen>
  )
}

/** What the search tells the user about the form. */
function Notices({ draft }: { draft: Draft }) {
  if (draft.limited) {
    return (
      <output className={`block ${notice}`}>
        <strong>That's a lot of links and photos for one hour.</strong> We didn't read this one. Fill it in below, or try again later.
      </output>
    )
  }
  return (
    <>
      {draft.kind === 'link' && !draft.result.found && (
        <output className={`block ${notice}`}>
          <strong>We couldn't read that link.</strong> Your link is saved below. Fill in what you can.
        </output>
      )}
      {draft.kind === 'link' && draft.result.found && <p>We filled this in from your link. Change anything that's wrong.</p>}
      {draft.kind === 'photo' && draft.result.found && <p>We guessed this from your photo. Change anything that's wrong.</p>}
      {draft.result.notes_cut && <p>Your text was long, so we kept the first {MAX_NOTES} characters.</p>}
    </>
  )
}
