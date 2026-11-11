import type { Gift } from './api'
import { joinNames } from './gifts'
import { GiftPicture } from './Giving'
import { groupByOccasion, GroupSection, titleWithQuantity } from './Received'
import { Screen } from './Screen'
import { useCollection } from './useCollection'
import { primary } from './ui'

/**
 * Given: the gifts the signed-in user gave, newest first, grouped by occasion,
 * with who each one went to (docs/spec.md, Gifts, Reveal). The gift is the
 * same record as the recipients' Received; the giver also sees their own note.
 * A parent can't see a child's Given.
 */
export function Given() {
  const { loaded } = useCollection<Gift>('/api/v2/gifts?giver_id=me&state=given&sort=-given_at')

  if (loaded.status === 'failed') {
    return (
      <Screen title="Given">
        <p role="alert">Could not load what you gave. Reload and try again.</p>
      </Screen>
    )
  }
  if (loaded.status === 'loading') return null

  return (
    <Screen title="Given">
      {loaded.rows.length === 0 && (
        <div className="flex max-w-xl flex-col gap-3">
          <h2 className="text-[1.75rem]/[1.2] font-extrabold">Nothing yet</h2>
          <p className="text-xl">After an occasion, the gifts you gave show up here. Only you can see this.</p>
          <a href="/giving" className={`${primary} flex items-center justify-center`}>
            Go to Giving
          </a>
        </div>
      )}
      {groupByOccasion(loaded.rows).map((group) => (
        <GroupSection key={group.key} group={group}>
          {group.gifts.map((g) => (
            <GivenRow key={g.id} gift={g} />
          ))}
        </GroupSection>
      ))}
    </Screen>
  )
}

/** "To Mom and Dad". A giver who couldn't see the list at reveal has no recipients to name, so it names the list. */
function toText(g: Gift): string {
  return `To ${g.recipients && g.recipients.length > 0 ? joinNames(g.recipients.map((r) => r.name)) : g.list.name}`
}

function GivenRow({ gift: g }: { gift: Gift }) {
  return (
    <li className="flex flex-col gap-3 border-b border-hair px-4 py-4 last:border-b-0 sm:px-0">
      <div className="flex items-center gap-4">
        <GiftPicture gift={g} />
        <div className="flex min-w-0 flex-col gap-1.5">
          <h3 className="text-xl font-extrabold wrap-break-word">{titleWithQuantity(g)}</h3>
          <p className="min-w-0 wrap-break-word">{toText(g)}</p>
          {g.from_label && <p className="min-w-0 wrap-break-word">From {g.from_label}</p>}
          {g.note && <p className="min-w-0 wrap-break-word">{g.note}</p>}
        </div>
      </div>
    </li>
  )
}
