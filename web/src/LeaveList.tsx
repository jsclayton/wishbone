import { leaveList, type Collection, type List, type Recipient } from './api'
import { Confirm } from './Confirm'
import { replaceTo } from './navigation'
import { Resource } from './Resource'
import { NotFound } from './Screen'

/**
 * The screen before a recipient leaves a list, or a parent makes their child
 * leave it. The button repeats the action ("Leave Our house", "Make Emma leave
 * Our house"). It warns when the recipient's row in `GET /lists/{id}/recipients`
 * has `sees_list_after_leaving` set: they would still see the list, as a giver,
 * with what's been claimed on it. After the leave, My lists opens
 * (docs/spec.md, Lists, Screens).
 */
export function LeaveList({ listId, userId, meId }: { listId: string; userId: string; meId: string }) {
  return (
    <Resource<List> path={`/api/v2/lists/${listId}`}>
      {(list) => (
        <Resource<Collection<Recipient>> path={`/api/v2/lists/${listId}/recipients`} all>
          {(recipients) => {
            const row = recipients.data.find((r) => r.user.id === userId)
            return row?.can_delete ? <LeaveConfirm list={list} row={row} self={userId === meId} /> : <NotFound />
          }}
        </Resource>
      )}
    </Resource>
  )
}

function LeaveConfirm({ list, row, self }: { list: List; row: Recipient; self: boolean }) {
  const name = row.user.name
  const warning = self
    ? "You'll see what's been claimed on this list, including gifts for you."
    : `${name} will see what's been claimed on this list, including gifts for ${name}.`
  return (
    <Confirm
      heading={self ? 'Leave this list?' : `Make ${name} leave this list?`}
      statement={self ? `You'll no longer be a recipient of ${list.name}.` : `${name} will no longer be a recipient of ${list.name}.`}
      details={row.sees_list_after_leaving ? [warning] : []}
      action={self ? `Leave ${list.name}` : `Make ${name} leave ${list.name}`}
      keepHref={`/lists/${list.id}`}
      keepLabel={self ? 'Stay on this list' : `Keep ${name} on this list`}
      onConfirm={async () => {
        await leaveList(list.id, row.user.id)
        replaceTo('/lists')
      }}
    />
  )
}
