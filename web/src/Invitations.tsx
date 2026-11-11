import { useEffect, useId, useState } from 'react'
import { acceptInvitation, deleteInvitation, fetchTotal, type Invitation, type User } from './api'
import { UserAvatar } from './Avatar'
import { useList } from './useList'
import { notice } from './Picker'
import { button } from './ui'
import { useCollection, type Loaded } from './useCollection'

/** "1 invitation", "3 invitations". */
export function invitationCount(n: number): string {
  return n === 1 ? '1 invitation' : `${n} invitations`
}

/** The address of the invitations to the user and to their children (`GET /invitations?user_id=`). */
function myInvitationsPath(me: User): string {
  return `/api/v2/invitations?${[me.id, ...me.children.map((c) => c.id)].map((id) => `user_id=${id}`).join('&')}`
}

/**
 * The pending invitations for the user and for their children. Home counts them
 * and My lists shows them (docs/spec.md, Lists, Screens).
 */
export function useMyInvitations(me: User): { loaded: Loaded<Invitation>; reload: () => void } {
  return useCollection<Invitation>(myInvitationsPath(me))
}

/**
 * How many invitations the user and their children have, for Home's button. It
 * asks for one row and reads the collection's `total_count`, so the count is
 * right however many there are. It is 0 until the answer comes, and when the
 * request fails.
 */
export function useInvitationTotal(me: User): number {
  const path = `${myInvitationsPath(me)}&limit=1`
  const [result, setResult] = useState<{ path: string; total: number } | null>(null)
  useEffect(() => {
    let live = true
    fetchTotal(path)
      .then((total) => live && setResult({ path, total }))
      .catch(() => live && setResult({ path, total: 0 }))
    return () => {
      live = false
    }
  }, [path])
  return result?.path === path ? result.total : 0
}

/** Whether the invitation is for the user or for one of their children. */
export function isForMe(inv: Invitation, me: User): boolean {
  return inv.user.id === me.id || me.children.some((c) => c.id === inv.user.id)
}

/**
 * Who was invited and by whom, for the screens that name the list too ("Mom
 * invited Emma to Cousins"). `list` is what the sentence ends with: the list's
 * name, or "this list" on the list's own page.
 */
function sentence(inv: Invitation, me: User, list: string): string {
  const who = inv.user.id === me.id ? 'you' : inv.user.name
  if (!inv.invited_by) return inv.user.id === me.id ? `You were invited to ${list}` : `${inv.user.name} was invited to ${list}`
  return `${inv.invited_by.name} invited ${who} to ${list}`
}

/** Accept, Decline and Withdraw: no confirmation, because each is easy to undo. A refusal shows its words. */
function useRespond(onChanged: () => void) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function run(act: () => Promise<{ ok: true } | { ok: false; detail: string }>) {
    setBusy(true)
    setError(null)
    try {
      const res = await act()
      if (res.ok) {
        onChanged()
        return
      }
      setError(res.detail)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return {
    busy,
    error,
    accept: (inv: Invitation) => run(async () => acceptInvitation(inv.id)),
    remove: (inv: Invitation) => run(async () => deleteInvitation(inv.id)),
  }
}

type Respond = ReturnType<typeof useRespond>

function Answer({ inv, respond }: { inv: Invitation; respond: Respond }) {
  return (
    <>
      {inv.can_accept && (
        <button type="button" className={button} disabled={respond.busy} onClick={() => void respond.accept(inv)}>
          Accept
        </button>
      )}
      {inv.can_delete && (
        <button type="button" className={button} disabled={respond.busy} onClick={() => void respond.remove(inv)}>
          Decline
        </button>
      )}
    </>
  )
}

function Refusal({ error }: { error: string | null }) {
  return (
    error && (
      <p role="alert" className={notice}>
        {error}
      </p>
    )
  )
}

/** The text of an invitation on My lists. It opens the list's page when the user can see the list. */
function InvitationText({ inv, me }: { inv: Invitation; me: User }) {
  // The list is visible to the user only if `GET /lists/{id}` answers (a summary doesn't make it so).
  const list = useList(inv.list.id)
  const text = sentence(inv, me, inv.list.name)
  return list ? (
    <a href={`/lists/${inv.list.id}`} className="font-semibold underline">
      {text}
    </a>
  ) : (
    <span className="font-semibold">{text}</span>
  )
}

/**
 * The Invitations part of My lists: the pending invitations for the user and for
 * their children, each with Accept and Decline (from `can_accept` and
 * `can_delete`). It shows only when some are pending. `onChanged` asks the
 * screen to load again, so an accepted list shows in the lists.
 */
export function MyInvitations({ me, onChanged }: { me: User; onChanged: () => void }) {
  const { loaded } = useMyInvitations(me)
  const respond = useRespond(onChanged)
  const heading = useId()
  if (loaded.status === 'failed') return <p role="alert">Could not load your invitations. Reload and try again.</p>
  if (loaded.status !== 'ready' || loaded.rows.length === 0) return null
  return (
    <section aria-labelledby={heading} className="flex max-w-xl flex-col gap-2">
      <h2 id={heading} className="text-[1.375rem] font-extrabold">
        Invitations
      </h2>
      <Refusal error={respond.error} />
      <ul aria-label="Invitations" className="flex flex-col">
        {loaded.rows.map((inv) => (
          <li key={inv.id} className="flex flex-col gap-2 border-t border-hair py-3 last:border-b">
            <InvitationText inv={inv} me={me} />
            <span className="flex gap-2">
              <Answer inv={inv} respond={respond} />
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}

/**
 * A pending invitation for the user or a child, at the top of the list's page:
 * "Mom invited you to this list", with Accept and Decline.
 */
export function PendingForMe({ invitations, me, onChanged }: { invitations: Invitation[]; me: User; onChanged: () => void }) {
  const respond = useRespond(onChanged)
  if (invitations.length === 0) return null
  return (
    <div className="flex max-w-xl flex-col gap-3">
      <Refusal error={respond.error} />
      {invitations.map((inv) => (
        <div key={inv.id} className="flex flex-col gap-2 rounded-md bg-tile p-4">
          <p className="text-xl font-semibold">{sentence(inv, me, 'this list')}</p>
          <div className="flex gap-2">
            <Answer inv={inv} respond={respond} />
          </div>
        </div>
      ))}
    </div>
  )
}

/**
 * The other pending invitations to the list, for a recipient: the invited user's
 * name and avatar and who invited them, with Withdraw from `can_delete`
 * (`GET /invitations?list_id=`, without the ones that `PendingForMe` shows).
 */
export function OtherInvitations({ invitations, onChanged }: { invitations: Invitation[]; onChanged: () => void }) {
  const respond = useRespond(onChanged)
  if (invitations.length === 0) return null
  return (
    <section className="flex max-w-xl flex-col gap-2">
      <Refusal error={respond.error} />
      <ul aria-label="Invitations" className="flex flex-col">
        {invitations.map((inv) => (
          <li key={inv.id} className="flex min-h-19 items-center justify-between gap-3 border-t border-hair py-2 last:border-b">
            <span className="flex min-w-0 items-center gap-3">
              <UserAvatar user={inv.user} size={40} />
              <span className="text-xl/tight font-semibold wrap-break-word">
                {inv.user.name}
                {inv.invited_by && ` · invited by ${inv.invited_by.name}`}
              </span>
            </span>
            {inv.can_delete && (
              <button
                type="button"
                className={`${button} shrink-0`}
                disabled={respond.busy}
                aria-label={`Withdraw ${inv.user.name}'s invitation`}
                onClick={() => void respond.remove(inv)}
              >
                Withdraw
              </button>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}
