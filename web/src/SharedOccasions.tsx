import { useCallback, useEffect, useId, useState, type ReactNode } from 'react'
import {
  attachGroup,
  createSharedOccasion,
  deleteOccasion,
  detachGroup,
  fetchAll,
  updateSharedOccasion,
  type Gift,
  type Group,
  type Occasion,
  type User,
} from './api'
import { Confirm } from './Confirm'
import { goBack, replaceTo } from './navigation'
import { describeRule, OccasionForm } from './OccasionForm'
import { UsersPart } from './People'
import { notice, Picker, row } from './Picker'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { button, buttonLink } from './ui'
import { countdown, daysUntil, longDate } from './Upcoming'
import { useCollection, type Loaded } from './useCollection'

const occasionPath = (id: string) => `/occasions/${id}`

/**
 * The Shared occasions screen, for the admin (docs/spec.md, Occasions,
 * Shared occasions): the shared occasions by name, each opening its page, and a
 * way to make one. Anyone else gets the missing-page message.
 */
export function SharedOccasions({ me }: { me: User }) {
  const { loaded } = useCollection<Occasion>(me.is_admin ? '/api/v2/occasions?kind=shared&sort=name' : null)
  if (!me.is_admin) return <NotFound />
  return (
    <Screen title="Shared occasions">
      <a href="/occasions/new" className={`${buttonLink} sm:max-w-md`}>
        New shared occasion
      </a>
      {loaded.status === 'failed' && <p role="alert">Could not load the shared occasions. Reload and try again.</p>}
      {loaded.status === 'ready' &&
        (loaded.rows.length === 0 ? (
          <p className="text-lg">No shared occasions yet.</p>
        ) : (
          <ul className="flex max-w-xl flex-col">
            {loaded.rows.map((o) => (
              <li key={o.id} className="border-t border-hair last:border-b">
                <a href={occasionPath(o.id)} className="flex min-h-19 flex-col justify-center gap-1 py-3">
                  <strong className="text-xl/tight font-extrabold">{o.name}</strong>
                  <span className="text-lg text-mute">{describeRule(o.rule)}</span>
                  {o.is_everyone && <span className="text-lg font-semibold">Everyone</span>}
                </a>
              </li>
            ))}
          </ul>
        ))}
    </Screen>
  )
}

/**
 * The switch that marks a shared occasion everyone. `was` is what the occasion
 * is now (nothing yet, for a new one), and says what the change would do.
 */
function EveryoneSwitch({ checked, onChange, was }: { checked: boolean; onChange: (on: boolean) => void; was: boolean | null }) {
  const hint = useId()
  const words =
    was === null
      ? 'Every user, now and later. Leave it off to attach groups afterwards.'
      : was
        ? 'Unmarking it leaves it attached to no groups.'
        : 'Marking it everyone detaches its groups.'
  return (
    <div className="flex flex-col">
      <label className="flex min-h-12 items-center gap-3 font-semibold">
        <input type="checkbox" aria-describedby={hint} checked={checked} onChange={(e) => onChange(e.target.checked)} />
        Everyone
      </label>
      <p id={hint} className="text-mute">
        {words}
      </p>
    </div>
  )
}

/** Make a shared occasion. The new occasion opens in place of this form, where the admin attaches groups. */
export function NewSharedOccasion({ me }: { me: User }) {
  const [everyone, setEveryone] = useState(false)
  if (!me.is_admin) return <NotFound />
  return (
    <Screen title="New shared occasion">
      <OccasionForm
        label="Create this shared occasion"
        extra={<EveryoneSwitch checked={everyone} onChange={setEveryone} was={null} />}
        onSubmit={async (input) => {
          const res = await createSharedOccasion({ ...input, is_everyone: everyone })
          if (!res.ok) return res.detail
          replaceTo(occasionPath(res.value.id))
          return null
        }}
      />
    </Screen>
  )
}

/** The shared occasion at a path, for a screen that only fits one the caller can use that way. Any other occasion is the missing-page message. */
function SharedOccasion({ occasionId, allowed, children }: { occasionId: string; allowed: (o: Occasion) => boolean; children: (o: Occasion) => ReactNode }) {
  return (
    <Resource<Occasion> path={`/api/v2/occasions/${occasionId}`}>
      {(o) => (o.kind === 'shared' && allowed(o) ? children(o) : <NotFound />)}
    </Resource>
  )
}

/** What a part of the page shows while its rows load, or when they don't. */
function Rows<T>({ loaded, children }: { loaded: Loaded<T>; children: (rows: T[]) => ReactNode }) {
  if (loaded.status === 'failed') return <p role="alert">Could not load this. Reload and try again.</p>
  if (loaded.status === 'loading') return null
  return children(loaded.rows)
}

/** "Sat Dec 6, 28 days", or "Already passed" for a one-time date that has gone. */
function DateLine({ occasion: o }: { occasion: Occasion }) {
  if (!o.next_date) return <p className="mt-1 font-mono">Already passed</p>
  return (
    <p className="mt-1 font-mono">
      <time dateTime={o.next_date}>
        {longDate(o.next_date)}, {countdown(daysUntil(o.next_date))}
      </time>
    </p>
  )
}

/**
 * How many of the viewer's own open gifts are for each user on this date of the
 * occasion (`GET /gifts?giver_id=me&state=open&occasion_id=`; a gift is for the
 * recipients of its list). `null` until the gifts are in, and when they can't
 * load: the page then marks nobody, rather than say "Nothing yet" wrongly.
 */
function useMarks(o: Occasion, me: User): Map<string, number> | null {
  const { loaded } = useCollection<Gift>(`/api/v2/gifts?giver_id=me&state=open&occasion_id=${o.id}`)
  if (loaded.status !== 'ready') return null
  const marks = new Map<string, number>()
  for (const g of loaded.rows) {
    if (g.occasion_date !== o.next_date) continue
    // A gift is for the other recipients of its list, never its giver.
    for (const r of g.recipients ?? []) if (r.id !== me.id) marks.set(r.id, (marks.get(r.id) ?? 0) + 1)
  }
  return marks
}

/** The people on an occasion, as the user who looks sees them: the parts of People. */
type OnIt = { children: User[]; parents: User[]; groups: { group: Group; users: User[] }[] }

/**
 * Who is on a shared occasion, in the parts that Home's People has: the user's
 * children and parents who are on it, then the user's own groups with the other
 * users of each who are on it (`GET /groups?includes_me=true`, then
 * `GET /users?occasion_id=&group_id=` for each group). So the admin gets only
 * the groups the admin is in, as in People. A user with a parent or a child asks
 * for every user the occasion applies to, once, to find which of them are on it.
 * `reload` asks again after the groups of the occasion change.
 */
function useOnIt(me: User, occasionId: string): { loaded: Loaded<OnIt>; reload: () => void } {
  const [attempt, setAttempt] = useState(0)
  const [result, setResult] = useState<{ key: string; loaded: Loaded<OnIt> }>({ key: '', loaded: { status: 'loading' } })
  const key = `${occasionId}/${attempt}`

  useEffect(() => {
    let live = true
    async function load(): Promise<OnIt> {
      const linked = me.children.length > 0 || me.parents.length > 0
      const [groups, onIt] = await Promise.all([
        fetchAll<Group>('/api/v2/groups?includes_me=true'),
        linked ? fetchAll<User>(`/api/v2/users?occasion_id=${occasionId}`) : Promise.resolve([]),
      ])
      const parts = await Promise.all(groups.map(async (group) => ({ group, users: await fetchAll<User>(`/api/v2/users?occasion_id=${occasionId}&group_id=${group.id}`) })))
      const byId = new Map(onIt.map((u) => [u.id, u]))
      // The summaries are in name order. Keep it, and drop whoever is not on it or the user can't see.
      const seen = (list: { id: string }[]) => list.flatMap((s) => byId.get(s.id) ?? [])
      return {
        children: seen(me.children),
        parents: seen(me.parents),
        groups: parts.map((p) => ({ ...p, users: p.users.filter((u) => u.id !== me.id) })).filter((p) => p.users.length > 0),
      }
    }
    load()
      .then((rows): Loaded<OnIt> => ({ status: 'ready', rows: [rows] }))
      .catch((): Loaded<OnIt> => ({ status: 'failed' }))
      .then((loaded) => live && setResult({ key, loaded }))
    return () => {
      live = false
    }
  }, [key, me.children, me.parents, me.id, occasionId])

  const reload = useCallback(() => setAttempt((n) => n + 1), [])
  return { loaded: result.key === key ? result.loaded : { status: 'loading' }, reload }
}

/**
 * "See who's on it": a shared occasion with its date and countdown, and the
 * people on it whom the viewer can see, in the parts of People ([useOnIt]). The
 * admin also gets the groups it is attached to, with Detach and Attach a group,
 * and Edit and Delete, from the occasion's `can_*` fields. A birthday and a custom
 * occasion have no such page.
 *
 * Each person is marked "N from you" (the viewer's own open gifts for them on
 * the occasion's date) or "Nothing yet" ([useMarks]).
 */
export function SharedOccasionPage({ occasionId, me }: { occasionId: string; me: User }) {
  return (
    <SharedOccasion occasionId={occasionId} allowed={() => true}>
      {(o) => <OccasionScreen occasion={o} me={me} />}
    </SharedOccasion>
  )
}

function OccasionScreen({ occasion: o, me }: { occasion: Occasion; me: User }) {
  const onIt = useOnIt(me, o.id)
  const marks = useMarks(o, me)
  const groups = useCollection<Group>(o.can_edit && !o.is_everyone ? `/api/v2/occasions/${o.id}/groups` : null)
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)

  async function detach(g: Group) {
    setBusy(true)
    setFailed(false)
    try {
      await detachGroup(o.id, g.id)
      groups.reload()
      onIt.reload()
    } catch {
      setFailed(true)
    }
    setBusy(false)
  }

  return (
    <Screen title={o.name} band={<DateLine occasion={o} />}>
      <div className="flex max-w-2xl flex-col gap-8">
        <section aria-labelledby="on-it" className="flex flex-col gap-3">
          <h2 id="on-it" className="text-[1.375rem]/normal font-extrabold">
            Who's on it
          </h2>
          <Rows loaded={onIt.loaded}>
            {([people]) =>
              people!.children.length === 0 && people!.parents.length === 0 && people!.groups.length === 0 ? (
                <p className="text-lg">Nobody else you can see is on this yet.</p>
              ) : (
                <div className="flex flex-col gap-3">
                  {people!.children.length > 0 && <UsersPart title="Your children" users={people!.children} marks={marks ?? undefined} />}
                  {people!.parents.length > 0 && <UsersPart title="Your parents" users={people!.parents} marks={marks ?? undefined} />}
                  {people!.groups.map(({ group, users }) => (
                    <UsersPart
                      key={group.id}
                      title={group.name}
                      note={group.through ? `through ${group.through.name}` : undefined}
                      users={users}
                      marks={marks ?? undefined}
                    />
                  ))}
                </div>
              )
            }
          </Rows>
        </section>

        {o.can_edit && (
          <section aria-labelledby="groups" className="flex flex-col">
            <h2 id="groups" className="mb-2 text-[1.375rem]/normal font-extrabold">
              Groups
            </h2>
            {o.is_everyone ? (
              <p className="text-lg">Everyone is on this occasion, including users who join later.</p>
            ) : (
              <Rows loaded={groups.loaded}>
                {(rows) =>
                  rows.length === 0 ? (
                    <p className="text-lg">Not attached to any group.</p>
                  ) : (
                    <ul>
                      {rows.map((g) => (
                        <li key={g.id} className={row}>
                          <span className="text-xl/tight font-semibold wrap-break-word">{g.name}</span>
                          <button type="button" className={`${button} shrink-0`} disabled={busy} aria-label={`Detach ${g.name}`} onClick={() => detach(g)}>
                            Detach
                          </button>
                        </li>
                      ))}
                    </ul>
                  )
                }
              </Rows>
            )}
            {failed && (
              <p role="alert" className={`${notice} mt-3`}>
                Something went wrong. Try again.
              </p>
            )}
            {o.can_attach_group && (
              <a href={`${occasionPath(o.id)}/groups`} className={`${buttonLink} mt-4`}>
                Attach a group
              </a>
            )}
          </section>
        )}

        {o.can_edit && (
          <a href={`${occasionPath(o.id)}/edit`} className={buttonLink}>
            Edit this occasion
          </a>
        )}
      </div>
      {o.can_delete && (
        <a href={`${occasionPath(o.id)}/delete`} className="mt-auto flex min-h-12 items-center self-center pt-10 text-lg font-semibold underline">
          Delete this occasion
        </a>
      )}
    </Screen>
  )
}

/** Change a shared occasion: its name, its date rule, and whether it is everyone. */
export function EditSharedOccasion({ occasionId }: { occasionId: string }) {
  return (
    <SharedOccasion occasionId={occasionId} allowed={(o) => o.can_edit}>
      {(o) => <EditScreen occasion={o} />}
    </SharedOccasion>
  )
}

function EditScreen({ occasion: o }: { occasion: Occasion }) {
  const was = o.is_everyone === true
  const [everyone, setEveryone] = useState(was)
  return (
    <Screen title="Edit a shared occasion">
      <OccasionForm
        label="Save this shared occasion"
        initial={{ name: o.name, rule: o.rule }}
        extra={<EveryoneSwitch checked={everyone} onChange={setEveryone} was={was} />}
        onSubmit={async (input) => {
          // The mark goes only when it changed: sending it clears or keeps the groups.
          const res = await updateSharedOccasion(o.id, everyone === was ? input : { ...input, is_everyone: everyone })
          if (!res.ok) return res.detail
          goBack(occasionPath(o.id))
          return null
        }}
      />
    </Screen>
  )
}

/** The screen before a shared occasion is deleted. Its attached groups go with it. */
export function DeleteSharedOccasion({ occasionId }: { occasionId: string }) {
  return (
    <SharedOccasion occasionId={occasionId} allowed={(o) => o.can_delete}>
      {(o) => (
        <Confirm
          heading={`Delete ${o.name}?`}
          statement={`${o.name} goes.`}
          details={["People won't see it any more.", 'Its groups stay.']}
          action="Delete this occasion"
          keepHref={occasionPath(o.id)}
          onConfirm={async () => {
            await deleteOccasion(o.id)
            replaceTo('/occasions')
          }}
        />
      )}
    </SharedOccasion>
  )
}

/** Attach a group: every group that the occasion is not attached to yet. */
export function AttachOccasionGroup({ occasionId }: { occasionId: string }) {
  return (
    <SharedOccasion occasionId={occasionId} allowed={(o) => o.can_attach_group}>
      {(o) => <GroupPicker occasion={o} />}
    </SharedOccasion>
  )
}

function GroupPicker({ occasion: o }: { occasion: Occasion }) {
  const attached = useCollection<Group>(`/api/v2/occasions/${o.id}/groups`)
  const all = useCollection<Group>('/api/v2/groups')
  if (attached.loaded.status === 'failed' || all.loaded.status === 'failed') {
    return (
      <Screen title={`Attach a group to ${o.name}`}>
        <p role="alert">Could not load the groups. Reload and try again.</p>
      </Screen>
    )
  }
  if (attached.loaded.status !== 'ready' || all.loaded.status !== 'ready') return null
  const there = new Set(attached.loaded.rows.map((g) => g.id))
  const choices = all.loaded.rows.filter((g) => !there.has(g.id)).map((g) => ({ id: g.id, name: g.name }))
  return (
    <Picker
      title={`Attach a group to ${o.name}`}
      empty={all.loaded.rows.length === 0 ? 'There is no group yet.' : `Every group is attached to ${o.name} already.`}
      choices={choices}
      backTo={occasionPath(o.id)}
      onAdd={async (c) => {
        const res = await attachGroup(o.id, c.id)
        return res.ok ? null : res.detail
      }}
    />
  )
}
