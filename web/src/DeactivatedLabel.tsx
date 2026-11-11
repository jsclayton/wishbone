import { isDeactivated, type User } from './api'

/**
 * "Deactivated", beside a user's name, for a viewer who gets the user's
 * `deactivated_at`: the user's parents and the admin (docs/spec.md,
 * Deactivation and deletion, Screens). It shows nothing for anyone else, who
 * never sees a deactivated user. A screen's heading passes a `className` that
 * keeps the heading's color.
 */
export function DeactivatedLabel({ user, className = 'text-base font-normal text-mute' }: { user: Pick<User, 'deactivated_at'>; className?: string }) {
  if (!isDeactivated(user)) return null
  return (
    <>
      {' '}
      <span className={className}>Deactivated</span>
    </>
  )
}
