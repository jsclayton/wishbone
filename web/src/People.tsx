import { useEffect, useId, useState } from 'react'
import { fetchAll, fullName, type Group, type User } from './api'
import { UserAvatar } from './Avatar'
import { DeactivatedLabel } from './DeactivatedLabel'

type Part = { group: Group; users: User[] }

/** What People shows: the user's children and parents, then the groups. */
type Loaded = { children: User[]; parents: User[]; groups: Part[] }

/**
 * Home's People: the user's children (Your children), their parents (Your
 * parents), and the groups they are in with the other users in each, from
 * `GET /groups?includes_me=true` and `GET /users?group_id=`, which returns the
 * users in a group directly or through nesting. A user opens their
 * page. A group with nobody else in it is left out, and so is the whole part when
 * nothing is left (docs/spec.md, Users and parents, Screens).
 *
 * The user's `children` and `parents` are summaries. Your children and Your
 * parents show those who are in `GET /users` (a deactivated parent isn't there
 * for the child), whether or not they share a group with the user. A child who
 * shares a group with their parent shows in both places. The call to
 * `GET /users` is made only when the user has a parent or a child.
 *
 * `includes_me=true` makes the admin's People show only the groups the admin
 * is in, as for any other user: Home is about giving, and the admin manages
 * groups from the Groups screen.
 */
export function People({ me }: { me: User }) {
  const [loaded, setLoaded] = useState<Loaded | 'failed' | undefined>(undefined)

  useEffect(() => {
    let live = true
    async function load(): Promise<Loaded> {
      const linked = me.children.length > 0 || me.parents.length > 0
      const [groups, visible] = await Promise.all([
        fetchAll<Group>('/api/v2/groups?includes_me=true'),
        linked ? fetchAll<User>('/api/v2/users') : Promise.resolve([]),
      ])
      const all = await Promise.all(groups.map(async (group) => ({ group, users: await fetchAll<User>(`/api/v2/users?group_id=${group.id}`) })))
      const byId = new Map(visible.map((u) => [u.id, u]))
      // The summaries are in name order. Keep it, and drop whoever the user can't see.
      const seen = (list: { id: string }[]) => list.flatMap((s) => byId.get(s.id) ?? [])
      return {
        children: seen(me.children),
        parents: seen(me.parents),
        groups: all.map((p) => ({ ...p, users: p.users.filter((u) => u.id !== me.id) })).filter((p) => p.users.length > 0),
      }
    }
    load()
      .then((result) => live && setLoaded(result))
      .catch(() => live && setLoaded('failed'))
    return () => {
      live = false
    }
  }, [me.id, me.children, me.parents])

  if (loaded === 'failed') return <p role="alert">Could not load people. Reload and try again.</p>
  if (!loaded || (loaded.children.length === 0 && loaded.parents.length === 0 && loaded.groups.length === 0)) return null

  return (
    <section aria-labelledby="people-heading" className="flex min-w-0 flex-col gap-1">
      <h2 id="people-heading" className="text-[1.375rem]/normal font-extrabold">
        People
      </h2>
      <div className="flex flex-col gap-3">
        {loaded.children.length > 0 && <UsersPart title="Your children" users={loaded.children} />}
        {loaded.parents.length > 0 && <UsersPart title="Your parents" users={loaded.parents} />}
        {loaded.groups.map(({ group, users }) => (
          <UsersPart key={group.id} title={group.name} note={group.through ? `through ${group.through.name}` : undefined} users={users} />
        ))}
      </div>
    </section>
  )
}

const badge = 'ml-auto shrink-0 rounded-sm px-2 py-0.5 text-[1rem]'

/** "2 from you" or "Nothing yet": what the viewer's own open gifts for a user come to. */
function Mark({ count }: { count: number }) {
  return count > 0 ? (
    <span className={`${badge} bg-bg font-semibold`}>{count} from you</span>
  ) : (
    <span className={`${badge} bg-alert font-extrabold text-on-alert`}>Nothing yet</span>
  )
}

/**
 * One part of People: a heading, an optional note, and the users as tiles. With
 * `marks`, which holds the count of the viewer's own open gifts for each user, a
 * tile says "N from you" or "Nothing yet" (the occasion's page, "See who's on it").
 */
export function UsersPart({ title, note, users, marks }: { title: string; note?: string; users: User[]; marks?: Map<string, number> }) {
  const heading = useId()
  return (
    <section aria-labelledby={heading} className="flex flex-col gap-1">
      <h3 id={heading} className="text-base font-semibold text-mute">
        {title}
      </h3>
      {note && <p className="text-mute">{note}</p>}
      <ul className="flex flex-wrap gap-2">
        {users.map((u) => (
          // Two to a row. A long name takes its own row rather than split a word.
          <li key={u.id} className="max-w-full flex-[0_1_calc(50%-0.25rem)]">
            <a href={`/users/${u.id}`} className="flex min-h-14.5 items-center gap-2.5 rounded-md bg-tile px-2.5 py-2 font-semibold wrap-break-word">
              <UserAvatar user={u} size={40} />
              <span>
                {fullName(u)}
                <DeactivatedLabel user={u} />
              </span>
              {marks && <Mark count={marks.get(u.id) ?? 0} />}
            </a>
          </li>
        ))}
      </ul>
    </section>
  )
}
