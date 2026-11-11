import type { ImageSummary } from './api'
import { initialsOf } from './initials'

/** The sizes an avatar is drawn at, in pixels. */
export type AvatarSize = 24 | 32 | 40 | 52 | 72 | 88 | 104 | 180

// Whole class names, so Tailwind finds them. Letters grow with the circle.
const sizes: Record<AvatarSize, string> = {
  24: 'size-6 text-[10px]',
  32: 'size-8 text-xs',
  40: 'size-10 text-sm',
  52: 'size-13 text-lg',
  72: 'size-18 text-2xl',
  88: 'size-22 text-3xl',
  104: 'size-26 text-4xl',
  180: 'size-45 text-[4.25rem]',
}

// Whole class names, so Tailwind finds them: the 104 look, under 700px tall.
const shortScreen = '[@media(height<700px)]:size-26 [@media(height<700px)]:text-4xl'

/**
 * A person's avatar: a circle with their photo, or their initials when they
 * have none. The initials are drawn by CSS and are not page text. The avatar is
 * decorative for screen readers: the name always sits next to it. Most screens
 * use `UserAvatar`, which works out the initials.
 */
export function Avatar({
  initials,
  url,
  size = 40,
  shrinkOnShortScreen = false,
}: {
  initials: string
  url?: string
  size?: AvatarSize
  /** Draw at 104px instead on screens under 700px tall. */
  shrinkOnShortScreen?: boolean
}) {
  const frame = `${sizes[size]}${shrinkOnShortScreen ? ` ${shortScreen}` : ''} shrink-0 rounded-full`
  if (url) return <img src={url} alt="" className={`${frame} object-cover`} />
  return (
    <span
      aria-hidden="true"
      data-initials={initials}
      className={`${frame} flex items-center justify-center bg-[#4D5A66] font-extrabold text-white before:content-[attr(data-initials)]`}
    />
  )
}

/**
 * The avatar of a user, or of a user summary, which has no last name: the
 * photo, or the initials of the names the user has. Wherever a user shows up
 * with their name, this sits next to it.
 */
export function UserAvatar({
  user,
  size,
  shrinkOnShortScreen,
}: {
  user: { name: string; last_name?: string | null; avatar: ImageSummary | null }
  size?: AvatarSize
  shrinkOnShortScreen?: boolean
}) {
  return <Avatar initials={initialsOf(user.name, user.last_name)} url={user.avatar?.url} size={size} shrinkOnShortScreen={shrinkOnShortScreen} />
}
