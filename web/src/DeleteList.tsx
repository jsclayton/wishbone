import { deleteList, type List } from './api'
import { Confirm } from './Confirm'
import { replaceTo } from './navigation'
import { Resource } from './Resource'
import { NotFound } from './Screen'

/**
 * The screen before deleting an extra list. The words are the same for every
 * list, so the screen shows a recipient nothing about gifts (docs/spec.md,
 * Lists, Screens). After the delete, My lists opens.
 */
export function DeleteList({ listId }: { listId: string }) {
  return (
    <Resource<List> path={`/api/v2/lists/${listId}`}>
      {(list) =>
        list.can_delete ? (
          <Confirm
            heading="Delete this list?"
            statement={`${list.name} and its items go.`}
            details={["People won't see it any more.", "You can't get the items back."]}
            action={`Delete ${list.name} and its items`}
            keepHref={`/lists/${list.id}`}
            onConfirm={async () => {
              await deleteList(list.id)
              replaceTo('/lists')
            }}
          />
        ) : (
          <NotFound />
        )
      }
    </Resource>
  )
}
