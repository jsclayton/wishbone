import { useEffect, useRef, useState } from 'react'
import { getTaggedItem, MAX_IMAGES, PHOTO_TYPES, updateItemChecked, uploadImage, type Checked, type ImageSummary, type Item } from './api'
import { goBack } from './navigation'
import { notice } from './Picker'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { buttonLink, primary } from './ui'

const imagesPath = (itemId: string) => `/items/${itemId}/images`

/** The images with the primary image first. The rest keep the order they were added in. */
export function primaryFirst(item: Pick<Item, 'images' | 'primary_image'>): ImageSummary[] {
  const primaryId = item.primary_image?.id
  return [...item.images.filter((i) => i.id === primaryId), ...item.images.filter((i) => i.id !== primaryId)]
}

/**
 * Every image of an item on its page, the primary image first (docs/spec.md,
 * Items, Screens). A user who can edit the list also gets the way to the screen
 * where the images change.
 */
export function ItemImages({ item }: { item: Item }) {
  const images = primaryFirst(item)
  if (images.length === 0 && !item.can_edit) return null
  return (
    <section aria-label="Images" className="flex max-w-xl flex-col gap-2">
      {images.length > 0 && (
        <ul className="grid grid-cols-2 gap-3 md:grid-cols-3">
          {images.map((img, i) => (
            <li
              key={img.id}
              className={`flex aspect-square items-center justify-center rounded-md bg-tile ${i === 0 ? 'col-span-2 row-span-2 md:col-span-3 md:row-span-1 md:aspect-[2/1]' : ''}`}
            >
              <img
                src={img.url}
                width={img.width}
                height={img.height}
                alt={`${item.title}: ${i + 1} of ${images.length}${img.id === item.primary_image?.id ? ', primary' : ''}`}
                className="size-full rounded-md object-contain p-1"
              />
            </li>
          ))}
        </ul>
      )}
      {item.can_edit && (
        <a href={imagesPath(item.id)} className={buttonLink}>
          {images.length > 0 ? `Change images (${images.length})` : 'Add an image'}
        </a>
      )}
    </section>
  )
}

/** What takes focus once the images change: the Remove or Make primary button at an index, or the Add tile. */
type Focus = { remove: number } | { primary: number } | 'add' | null

/**
 * The screen where a user who can edit the list manages an item's images: add,
 * remove, and pick the primary image. Each change saves at once, with
 * `PATCH /items/{id}` and `image_ids` or `primary_image_id`. There is no way to
 * reorder the images (docs/spec.md, Items, Screens). Done goes back, as the
 * browser's Back does.
 */
export function ItemImagesScreen({ itemId }: { itemId: string }) {
  return (
    <Resource<Item> path={`/api/v2/items/${itemId}`}>
      {(loaded, etag) => (loaded.can_edit ? <Manage key={loaded.id} loaded={loaded} loadedTag={etag} /> : <NotFound />)}
    </Resource>
  )
}

function Manage({ loaded, loadedTag }: { loaded: Item; loadedTag: string | null }) {
  const [item, setItem] = useState(loaded)
  // The version the images on screen are from. A change only applies to this version (If-Match).
  const [etag, setEtag] = useState(loadedTag)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // Set by a change that worked, and used once the new images render.
  const focus = useRef<Focus>(null)
  const removes = useRef<(HTMLButtonElement | null)[]>([])
  const makePrimaries = useRef<(HTMLButtonElement | null)[]>([])
  const addTile = useRef<HTMLButtonElement>(null)
  const file = useRef<HTMLInputElement>(null)
  const images = item.images
  const canAdd = images.length < MAX_IMAGES

  // A pressed control can be gone after a change, so focus moves on.
  useEffect(() => {
    const target = focus.current
    focus.current = null
    if (target === null) return
    if (target === 'add') (addTile.current ?? removes.current[images.length - 1])?.focus()
    else if ('primary' in target) (makePrimaries.current[target.primary] ?? removes.current[target.primary])?.focus()
    else {
      const i = Math.min(target.remove, images.length - 1)
      ;(i >= 0 ? removes.current[i] : addTile.current)?.focus()
    }
  }, [images])

  /** One change at a time. The controls use `aria-disabled`, so a pressed one keeps focus. */
  async function change(send: (etag: string | null) => Promise<Checked>, after: Focus) {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const res = await send(etag)
      if (res.ok) {
        focus.current = after
        setItem(res.item)
        setEtag(res.etag)
      } else if (res.stale) {
        // Someone else changed the item. Show it as it is now, and let the user decide again.
        const fresh = await getTaggedItem(item.id)
        if (fresh) {
          setItem(fresh.item)
          setEtag(fresh.etag)
        }
        setError('This item changed while you were looking. We loaded it again. Check the images, then try again.')
      } else setError(res.detail)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  const ids = images.map((i) => i.id)
  return (
    <Screen title="Images">
      <p className="max-w-xl">Keep the images that show what you want. People see the primary image first.</p>
      <ul aria-label="Images" className="grid grid-cols-2 gap-3 md:grid-cols-[repeat(auto-fill,minmax(12.5rem,1fr))]">
        {images.map((img, i) => {
          const isPrimary = img.id === item.primary_image?.id
          return (
            <li key={img.id} className="flex min-w-0 flex-col gap-1.5">
              <div className={`relative flex aspect-square items-center justify-center rounded-md bg-tile ${isPrimary ? 'outline-3 -outline-offset-3 outline-ink' : ''}`}>
                <img
                  src={img.url}
                  alt={`${item.title}: ${i + 1} of ${images.length}${isPrimary ? ', primary' : ''}`}
                  className="size-full rounded-md object-contain p-1"
                />
                <button
                  type="button"
                  ref={(el) => {
                    removes.current[i] = el
                  }}
                  aria-label={`Remove image ${i + 1}`}
                  aria-disabled={busy}
                  className="absolute top-0 right-0 flex size-12 items-center justify-center text-ink"
                  onClick={() => void change((tag) => updateItemChecked(item.id, { image_ids: ids.filter((id) => id !== img.id) }, tag), { remove: i })}
                >
                  <span className="flex size-8.5 items-center justify-center rounded-full border border-hair bg-bg">
                    <svg width="18" height="18" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" aria-hidden="true">
                      <path d="M4 4 L16 16" />
                      <path d="M16 4 L4 16" />
                    </svg>
                  </span>
                </button>
              </div>
              {isPrimary ? (
                <p className="flex min-h-12 items-center justify-center font-extrabold">Primary image</p>
              ) : (
                <button
                  type="button"
                  ref={(el) => {
                    makePrimaries.current[i] = el
                  }}
                  aria-label={`Make image ${i + 1} the primary image`}
                  aria-disabled={busy}
                  className="min-h-12 rounded-md bg-tile px-2 font-extrabold text-ink"
                  onClick={() => void change((tag) => updateItemChecked(item.id, { primary_image_id: img.id }, tag), { primary: i })}
                >
                  Make primary
                </button>
              )}
            </li>
          )
        })}
        {canAdd && (
          <li className="flex min-w-0 flex-col">
            <button
              type="button"
              ref={addTile}
              aria-disabled={busy}
              className="aspect-square rounded-md border-2 border-dashed border-mute p-2 font-extrabold text-ink"
              onClick={() => !busy && file.current?.click()}
            >
              Add an image
            </button>
            <input
              ref={file}
              type="file"
              hidden
              accept={PHOTO_TYPES}
              aria-label="Choose an image to add"
              onChange={(e) => {
                const input = e.currentTarget
                const chosen = input.files?.[0]
                if (!chosen) return
                void change(async (tag) => {
                  const up = await uploadImage(chosen)
                  if (!up.ok) return { ok: false, stale: false, detail: up.detail }
                  return updateItemChecked(item.id, { image_ids: [...ids, up.value.id] }, tag)
                }, 'add').finally(() => {
                  input.value = '' // so choosing the same file again still fires
                })
              }}
            />
          </li>
        )}
      </ul>
      {!canAdd && <p className="text-mute">An item can have {MAX_IMAGES} images. Remove one to add another.</p>}
      {error && (
        <p role="alert" className={notice}>
          {error}
        </p>
      )}
      <div className="sticky bottom-0 mt-auto border-t border-hair bg-bg pt-3 pb-[max(0.5rem,env(safe-area-inset-bottom))] md:static md:border-0">
        <button type="button" className={`${primary} w-full md:w-auto md:min-w-50`} onClick={() => goBack(`/items/${item.id}`)}>
          Done
        </button>
      </div>
    </Screen>
  )
}
