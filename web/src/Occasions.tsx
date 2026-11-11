import type { ReactNode } from 'react'
import { createOccasion, deleteOccasion, updateOccasion, type Occasion, type User } from './api'
import { months } from './BirthdayFields'
import { Confirm } from './Confirm'
import { goBack, replaceTo } from './navigation'
import { describeRule, OccasionForm } from './OccasionForm'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { buttonLink, link } from './ui'
import { useCollection } from './useCollection'

/** "May 6, 2026" from "2026-05-06". */
function formatDate(iso: string): string {
  const [y, m = 1, d] = iso.split('-').map(Number)
  return `${months[m - 1]!.slice(0, 3)} ${d}, ${y}`
}

const profilePath = (userId: string) => `/users/${userId}/profile`
const occasionPath = (userId: string, occasionId: string) => `/users/${userId}/occasions/${occasionId}`

/** The date a row shows: the next one, or, for a one-time occasion that has passed, its own. */
function rowDate(o: Occasion): string {
  if (o.next_date) return formatDate(o.next_date)
  const { year, month, day } = o.rule
  return year && day ? formatDate(`${year}-${month}-${day}`) : ''
}

/** The line under an occasion's name on the profile screen. */
function note(o: Occasion): string {
  switch (o.kind) {
    case 'birthday':
      return 'From the birthday above'
    case 'shared':
      return o.is_everyone ? 'Shared with everyone' : 'Shared occasion'
    case 'custom':
      return o.next_date ? describeRule(o.rule) : 'Already passed'
  }
}

/**
 * A user's occasions, for the profile screen (docs/spec.md, Occasions,
 * Screens): the birthday and the custom occasions that `GET /occasions` gives
 * the viewer, in the order it gives them. The admin gets the birthday only,
 * because the admin's access shows no custom occasion. Edit shows on a row from
 * its `can_edit`, and Add an occasion from the user's `can_add_occasion`. The
 * part shows its own failure and leaves the rest of the screen.
 */
export function OccasionsPart({ user }: { user: User }) {
  const { loaded } = useCollection<Occasion>(`/api/v2/occasions?user_id=${user.id}`)
  if (loaded.status === 'failed') return <p role="alert">Could not load occasions. Reload and try again.</p>
  if (loaded.status !== 'ready') return null
  return (
    <section aria-labelledby="occasions" className="flex max-w-xl flex-col gap-4">
      <h2 id="occasions" className="text-2xl font-extrabold">
        Occasions
      </h2>
      <ul aria-label="Occasions" className="flex flex-col border-t border-hair">
        {loaded.rows.map((o) => (
          <li key={o.id} className="relative flex items-center gap-4 border-b border-hair py-3">
            <time dateTime={o.next_date ?? undefined} className="w-32 shrink-0">
              {rowDate(o)}
            </time>
            <span className="flex flex-col">
              <strong className="font-extrabold">{o.name}</strong>
              <span className="text-mute">{note(o)}</span>
            </span>
            {o.kind === 'shared' && (
              <a href={`/occasions/${o.id}`} className="ml-auto flex min-h-12 min-w-11 items-center font-semibold underline after:absolute after:inset-0">
                See who's on it <span className="sr-only">{o.name}</span>
              </a>
            )}
            {o.kind === 'custom' && o.can_edit && (
              <a
                href={occasionPath(user.id, o.id)}
                className="ml-auto flex min-h-12 min-w-11 items-center font-semibold underline after:absolute after:inset-0"
              >
                Edit <span className="sr-only">{o.name}</span>
              </a>
            )}
          </li>
        ))}
      </ul>
      {user.can_add_occasion && (
        <a href={`/users/${user.id}/occasions/new`} className={buttonLink}>
          Add an occasion
        </a>
      )}
    </section>
  )
}

/**
 * Add an occasion: a custom occasion for the user, or for the child whose
 * profile the viewer opened. The viewer must have `can_add_occasion` for the
 * user, or the screen is the missing-page message. It goes back to the profile
 * after a save.
 */
export function AddOccasion({ userId, me }: { userId: string; me: User }) {
  return (
    <Resource<User> path={`/api/v2/users/${userId}`}>
      {(user) =>
        user.can_add_occasion ? (
          <Screen title={user.id === me.id ? 'Add an occasion' : `Add an occasion for ${user.name}`}>
            <OccasionForm
              label="Add this occasion"
              onSubmit={async (input) => {
                const res = await createOccasion(user.id, input)
                if (!res.ok) return res.detail
                goBack(profilePath(user.id))
                return null
              }}
            />
          </Screen>
        ) : (
          <NotFound />
        )
      }
    </Resource>
  )
}

/**
 * The occasion at this address, for the screens that change it: a custom
 * occasion of the user in the path that the viewer can change. A birthday, an
 * occasion of someone else, and an occasion the viewer can't change are the
 * missing-page message, like one the server doesn't show.
 */
function ChangeableOccasion({ userId, occasionId, children }: { userId: string; occasionId: string; children: (o: Occasion) => ReactNode }) {
  return (
    <Resource<Occasion> path={`/api/v2/occasions/${occasionId}`}>
      {(o) => (o.kind === 'custom' && o.can_edit && o.user?.id === userId ? children(o) : <NotFound />)}
    </Resource>
  )
}

/** Change a custom occasion, with a quiet link to delete it, from `can_delete`. */
export function EditOccasion({ userId, occasionId }: { userId: string; occasionId: string }) {
  return (
    <ChangeableOccasion userId={userId} occasionId={occasionId}>
      {(o) => (
        <Screen title="Edit an occasion">
          <OccasionForm
            label="Save this occasion"
            initial={{ name: o.name, rule: o.rule }}
            onSubmit={async (input) => {
              const res = await updateOccasion(o.id, input)
              if (!res.ok) return res.detail
              goBack(profilePath(userId))
              return null
            }}
          />
          {o.can_delete && (
            <a href={`${occasionPath(userId, o.id)}/delete`} className={`${link} max-w-xl justify-center font-semibold`}>
              Delete this occasion
            </a>
          )}
        </Screen>
      )}
    </ChangeableOccasion>
  )
}

/** The screen before a custom occasion is deleted. */
export function DeleteOccasion({ userId, occasionId }: { userId: string; occasionId: string }) {
  return (
    <Resource<Occasion> path={`/api/v2/occasions/${occasionId}`}>
      {(o) =>
        o.kind === 'custom' && o.can_delete && o.user?.id === userId ? (
          <Confirm
            heading="Delete this occasion?"
            statement={`${o.name} goes.`}
            details={["People won't see it any more.", 'You can add it again later.']}
            action="Delete this occasion"
            keepHref={occasionPath(userId, o.id)}
            onConfirm={async () => {
              await deleteOccasion(o.id)
              replaceTo(profilePath(userId))
            }}
          />
        ) : (
          <NotFound />
        )
      }
    </Resource>
  )
}
