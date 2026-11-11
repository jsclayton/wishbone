import type { ReactNode } from 'react'
import type { List, User } from './api'
import { listParts } from './listOrder'
import { select } from './ui'
import { useCollection } from './useCollection'

/**
 * The List choice (docs/spec.md, Items, Screens): where the add screen puts
 * an item, and where the edit form moves one. When the user can edit more than
 * one list (`GET /lists?can_edit=true`), it is a dropdown of the lists of the
 * My lists screen, in the same order, a group for each person. `value` is the
 * ID of the chosen list: the screen starts it on the list the add button was
 * on (the primary list for the box on Home), or on the item's own list. When the
 * user can edit only one list, or the lists can't be loaded, there is no choice
 * and `fallback` shows instead. `onChange` gets the ID of the list the user
 * picked.
 */
export function ListChoice({
  me,
  value,
  onChange,
  fallback = null,
}: {
  me: User
  value: string
  onChange: (listId: string) => void
  fallback?: ReactNode
}) {
  const { loaded } = useCollection<List>('/api/v2/lists?can_edit=true')
  const parts = loaded.status === 'ready' ? listParts(loaded.rows, me) : []
  if (parts.reduce((n, p) => n + p.lists.length, 0) < 2) return fallback
  return (
    <label className="flex max-w-xl flex-col gap-1 font-semibold">
      List
      <select className={`${select} font-normal`} value={value} onChange={(e) => onChange(e.target.value)}>
        {parts.map((part) => (
          <optgroup key={part.label} label={part.label}>
            {part.lists.map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
          </optgroup>
        ))}
      </select>
    </label>
  )
}
