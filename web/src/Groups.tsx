import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from 'react'
import {
  addGroupToGroup,
  addUserToGroup,
  createGroup,
  deleteGroup,
  fullName,
  isDeactivated,
  removeGroupFromGroup,
  removeUserFromGroup,
  renameGroup,
  type Collection,
  type Group,
  type User,
} from './api'
import { UserAvatar } from './Avatar'
import { DeactivatedLabel } from './DeactivatedLabel'
import { Confirm } from './Confirm'
import { replaceTo } from './navigation'
import { notice, Picker, row } from './Picker'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { aloneLink, button, buttonLink, field, primary } from './ui'
import { useCollection, type Loaded } from './useCollection'

const groupPath = (id: string) => `/groups/${id}`

/**
 * Groups: the groups the user is in, from `GET /groups`. For the admin that is
 * every group, with a way to create one (docs/routes.md, Groups). A group
 * opens its page.
 */
export function Groups({ me }: { me: User }) {
  return (
    <Resource<Collection<Group>> path="/api/v2/groups" all>
      {(groups) => {
        if (groups.data.length === 0) {
          return (
            <Screen title="Groups">
              <div className="flex max-w-xl flex-col gap-4">
                <h2 className="text-[1.75rem]/tight font-extrabold">No groups yet</h2>
                <p className="text-xl">People can only see each other when they're in the same group.</p>
                {me.is_admin ? (
                  <a href="/groups/new" className={primary + ' flex items-center justify-center'}>
                    Create a group
                  </a>
                ) : (
                  <p className="text-xl">The admin can add you to one.</p>
                )}
              </div>
            </Screen>
          )
        }
        return (
          <Screen title="Groups">
            {me.is_admin && (
              <a href="/groups/new" className={`${primary} flex items-center justify-center sm:max-w-md`}>
                Create a group
              </a>
            )}
            <ul className="flex max-w-xl flex-col">
              {groups.data.map((g) => (
                <li key={g.id} className="border-t border-hair last:border-b">
                  <a href={groupPath(g.id)} className="flex min-h-19 flex-col justify-center gap-1 py-3">
                    <strong className="text-xl/tight font-extrabold">{g.name}</strong>
                    {note(g, me) && <span className="text-lg text-mute">{note(g, me)}</span>}
                  </a>
                </li>
              ))}
            </ul>
          </Screen>
        )
      }}
    </Resource>
  )
}

/**
 * How the user is in a group. The admin sees groups they are not in, and the
 * group alone can't say whether they are in it directly, so they get a note only
 * for a group they are in through another.
 */
function note(g: Group, me: User): string {
  if (g.through) return `You're in this group through ${g.through.name}.`
  return me.is_admin ? '' : "You're in this group."
}

/** Create a group. The new group opens in place of this form, so Back goes to Groups. */
export function NewGroup({ me }: { me: User }) {
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  if (!me.is_admin) return <NotFound />

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (!name.trim()) {
      setError('Enter a group name.')
      return
    }
    setBusy(true)
    try {
      const res = await createGroup(name.trim())
      if (res.ok) {
        replaceTo(groupPath(res.value.id))
        return
      }
      setError(res.detail)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <Screen title="Create a group">
      <form onSubmit={submit} noValidate className="flex max-w-md flex-col gap-5">
        <label className="flex flex-col gap-1 font-semibold">
          Group name
          <input className={`${field} font-normal`} value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        {error && (
          <p role="alert" className={notice}>
            {error}
          </p>
        )}
        <button type="submit" className={primary} disabled={busy}>
          Create a group
        </button>
      </form>
    </Screen>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  const heading = useId()
  return (
    <section aria-labelledby={heading} className="flex flex-col">
      <h2 id={heading} className="mb-2 text-[1.375rem]/normal font-extrabold">
        {title}
      </h2>
      {children}
    </section>
  )
}

/** What a part of the group screen shows while its rows load, or when they don't. */
function Rows<T>({ loaded, children }: { loaded: Loaded<T>; children: (rows: T[]) => ReactNode }) {
  if (loaded.status === 'failed') return <p role="alert">Could not load this. Reload and try again.</p>
  if (loaded.status === 'loading') return null
  return children(loaded.rows)
}

/** The name of a user as a group lists them, marked when it is the signed-in user. */
const nameOf = (me: User, u: User) => (u.id === me.id ? `${fullName(u)} (you)` : fullName(u))

/** The Rename form: the name filled in and focused, with Save and Cancel. */
function RenameForm({ group, onSaved, onClose }: { group: Group; onSaved: (g: Group) => void; onClose: () => void }) {
  const [name, setName] = useState(group.name)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => input.current?.focus(), [])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const res = await renameGroup(group.id, name.trim())
      if (res.ok) {
        onSaved(res.value)
        onClose()
        return
      }
      setError(res.detail)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <form onSubmit={save} noValidate className="flex max-w-md flex-col gap-4">
      <label className="flex flex-col gap-1 font-semibold">
        Group name
        <input ref={input} className={`${field} font-normal`} value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      {error && (
        <p role="alert" className={notice}>
          {error}
        </p>
      )}
      <div className="flex gap-3">
        <button type="submit" className={`${primary} flex-1`} disabled={busy}>
          Save
        </button>
        <button type="button" className={`${button} flex-1`} disabled={busy} onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  )
}

/**
 * A group: its users and, for the admin, the groups inside it, each with what
 * the admin can change, from the group's `can_*` fields. A user in the group sees
 * its users and nothing to change.
 */
export function GroupPage({ groupId, me }: { groupId: string; me: User }) {
  return <Resource<Group> path={`/api/v2/groups/${groupId}`}>{(group) => <GroupScreen initial={group} me={me} />}</Resource>
}

function GroupScreen({ initial, me }: { initial: Group; me: User }) {
  // The group as the last rename left it. Its users and groups load apart.
  const [group, setGroup] = useState(initial)
  const [renaming, setRenaming] = useState(false)
  const [busy, setBusy] = useState(false)
  // The part a refused change came from, so the message shows where it was tapped.
  const [error, setError] = useState<string | null>(null)
  const users = useCollection<User>(`/api/v2/groups/${group.id}/users`)
  const inside = useCollection<Group>(group.can_add_group ? `/api/v2/groups/${group.id}/groups` : null)
  // Set when the Rename form closes, so focus goes back to the Rename button.
  const refocus = useRef(false)
  const renameButton = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (renaming || !refocus.current) return
    refocus.current = false
    renameButton.current?.focus()
  }, [renaming])

  async function remove(section: string, run: () => Promise<void>, reload: () => void) {
    setBusy(true)
    setError(null)
    try {
      await run()
      reload()
    } catch {
      setError(section)
    }
    setBusy(false)
  }

  const failure = (section: string) =>
    error === section && (
      <p role="alert" className={`${notice} mt-3`}>
        Something went wrong. Try again.
      </p>
    )

  return (
    <Screen
      title={group.name}
      action={
        group.can_edit &&
        !renaming && (
          <button type="button" ref={renameButton} className={`${aloneLink} font-semibold`} onClick={() => setRenaming(true)}>
            Rename
          </button>
        )
      }
    >
      {renaming && (
        <RenameForm
          group={group}
          onSaved={setGroup}
          onClose={() => {
            refocus.current = true
            setRenaming(false)
          }}
        />
      )}
      <div className="flex max-w-2xl flex-col gap-8">
        <Section title="Users">
          <Rows loaded={users.loaded}>
            {(rows) =>
              rows.length === 0 ? (
                <p className="text-lg">No users yet.</p>
              ) : (
                <ul>
                  {rows.map((u) => (
                    <li key={u.id} className={row}>
                      <a href={`/users/${u.id}`} className="flex min-w-0 items-center gap-3 text-xl/tight font-semibold wrap-break-word">
                        <UserAvatar user={u} size={40} />
                        <span>
                          {nameOf(me, u)}
                          <DeactivatedLabel user={u} />
                        </span>
                      </a>
                      {/* Deactivation changes no membership: the server refuses to remove a deactivated user. */}
                      {group.can_add_user && !isDeactivated(u) && (
                        <button
                          type="button"
                          className={`${button} shrink-0`}
                          disabled={busy}
                          aria-label={`Remove ${fullName(u)}`}
                          onClick={() => remove('users', () => removeUserFromGroup(group.id, u.id), users.reload)}
                        >
                          Remove
                        </button>
                      )}
                    </li>
                  ))}
                </ul>
              )
            }
          </Rows>
          {failure('users')}
          {group.can_add_user && (
            <a href={`${groupPath(group.id)}/users`} className={`${buttonLink} mt-4`}>
              Add a user
            </a>
          )}
        </Section>

        {group.can_add_group && (
          <Section title="Groups inside this one">
            <Rows loaded={inside.loaded}>
              {(rows) =>
                rows.length === 0 ? (
                  <p className="text-lg">No groups inside yet.</p>
                ) : (
                  <ul>
                    {rows.map((g) => (
                      <li key={g.id} className={row}>
                        <span className="text-xl/tight font-semibold wrap-break-word">{g.name}</span>
                        <button
                          type="button"
                          className={`${button} shrink-0`}
                          disabled={busy}
                          aria-label={`Remove ${g.name}`}
                          onClick={() => remove('groups', () => removeGroupFromGroup(group.id, g.id), inside.reload)}
                        >
                          Remove
                        </button>
                      </li>
                    ))}
                  </ul>
                )
              }
            </Rows>
            {failure('groups')}
            <a href={`${groupPath(group.id)}/groups`} className={`${buttonLink} mt-4`}>
              Add a group
            </a>
          </Section>
        )}
      </div>
      {group.can_delete && (
        <a href={`${groupPath(group.id)}/delete`} className="mt-auto flex min-h-12 items-center self-center pt-10 text-lg font-semibold underline">
          Delete this group
        </a>
      )}
    </Screen>
  )
}

/** The group at a path, for a screen only the admin can use: anyone else gets the missing-page message. */
function Managed({ groupId, allowed, children }: { groupId: string; allowed: (g: Group) => boolean; children: (g: Group) => ReactNode }) {
  return <Resource<Group> path={`/api/v2/groups/${groupId}`}>{(group) => (allowed(group) ? children(group) : <NotFound />)}</Resource>
}

/** Add a user: every user who is not directly in the group yet. */
export function AddGroupUser({ groupId, me }: { groupId: string; me: User }) {
  return (
    <Managed groupId={groupId} allowed={(g) => g.can_add_user}>
      {(group) => <UserPicker group={group} me={me} />}
    </Managed>
  )
}

function UserPicker({ group, me }: { group: Group; me: User }) {
  const inGroup = useCollection<User>(`/api/v2/groups/${group.id}/users`)
  const everyone = useCollection<User>('/api/v2/users')
  if (inGroup.loaded.status === 'failed' || everyone.loaded.status === 'failed') {
    return (
      <Screen title={`Add a user to ${group.name}`}>
        <p role="alert">Could not load the users. Reload and try again.</p>
      </Screen>
    )
  }
  if (inGroup.loaded.status !== 'ready' || everyone.loaded.status !== 'ready') return null
  const there = new Set(inGroup.loaded.rows.map((u) => u.id))
  const choices = everyone.loaded.rows.filter((u) => !there.has(u.id) && !isDeactivated(u)).map((u) => ({ id: u.id, name: nameOf(me, u), user: u }))
  return (
    <Picker
      title={`Add a user to ${group.name}`}
      empty={`Everyone is in ${group.name} already.`}
      choices={choices}
      backTo={groupPath(group.id)}
      onAdd={async (c) => {
        const res = await addUserToGroup(group.id, c.id)
        return res.ok ? null : res.detail
      }}
    />
  )
}

/** What the admin is told when the group they picked already holds the one they add to. */
function loopMessage(outer: string, inner: string): ReactNode {
  return (
    <>
      <strong className="font-extrabold">Can't add {inner}.</strong> {outer} is already inside {inner}, and a group can't end up inside itself.
    </>
  )
}

/** Add a group: every other group that is not directly inside this one yet. */
export function AddSubgroup({ groupId }: { groupId: string }) {
  return (
    <Managed groupId={groupId} allowed={(g) => g.can_add_group}>
      {(group) => <GroupPicker group={group} />}
    </Managed>
  )
}

function GroupPicker({ group }: { group: Group }) {
  const inside = useCollection<Group>(`/api/v2/groups/${group.id}/groups`)
  const all = useCollection<Group>('/api/v2/groups')
  if (inside.loaded.status === 'failed' || all.loaded.status === 'failed') {
    return (
      <Screen title={`Add a group to ${group.name}`}>
        <p role="alert">Could not load the groups. Reload and try again.</p>
      </Screen>
    )
  }
  if (inside.loaded.status !== 'ready' || all.loaded.status !== 'ready') return null
  const there = new Set(inside.loaded.rows.map((g) => g.id))
  const others = all.loaded.rows.filter((g) => g.id !== group.id)
  const choices = others.filter((g) => !there.has(g.id)).map((g) => ({ id: g.id, name: g.name }))
  return (
    <Picker
      title={`Add a group to ${group.name}`}
      empty={others.length === 0 ? 'There is no other group to add.' : `Every other group is inside ${group.name} already.`}
      choices={choices}
      backTo={groupPath(group.id)}
      onAdd={async (c) => {
        const res = await addGroupToGroup(group.id, c.id)
        if (res.ok) return null
        return res.loop ? loopMessage(group.name, c.name) : res.detail
      }}
    />
  )
}

/** Delete a group: only the group and its places in the nesting go. */
export function DeleteGroup({ groupId }: { groupId: string }) {
  return (
    <Managed groupId={groupId} allowed={(g) => g.can_delete}>
      {(group) => (
        <Confirm
          heading={`Delete ${group.name}?`}
          statement="Only the group goes. Its users and the groups inside it stay."
          details={["Users who share only this group can't see each other any more."]}
          action="Delete this group"
          keepHref={groupPath(group.id)}
          onConfirm={async () => {
            await deleteGroup(group.id)
            replaceTo('/groups')
          }}
        />
      )}
    </Managed>
  )
}
