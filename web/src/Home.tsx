import { useEffect, useState, type ReactNode } from 'react'
import { getUser, type Occasion, type User } from './api'
import { UserAvatar } from './Avatar'
import { invitationCount, useInvitationTotal } from './Invitations'
import { NotBoughtYet } from './NotBoughtYet'
import { PasteBox } from './PasteBox'
import { People } from './People'
import { Screen } from './Screen'
import { buttonLink } from './ui'
import type { Loaded } from './useCollection'
import { ComingUp, countdown, daysUntil, longDate, nextOccasions, occasionName, occasionPath, useUpcoming, type Upcoming } from './Upcoming'

const bigWords = 'text-[3.25rem]/none font-extrabold tracking-[-0.03em]'
const bigCount = 'text-[5.25rem]/none font-extrabold tracking-[-0.03em] lg:text-[6.5rem]/none'

function BandText({ eyebrow, big, large, children }: { eyebrow: ReactNode; big: string; large?: boolean; children?: ReactNode }) {
  return (
    <div className="mt-3 flex flex-col">
      <p className="font-mono text-band-mute">{eyebrow}</p>
      <p className={large ? bigCount : bigWords}>{big}</p>
      {children && <div className="mt-2 text-2xl/tight font-semibold">{children}</div>}
    </div>
  )
}

const bandLink = 'flex min-h-14.5 items-center justify-center rounded-md bg-on-band px-6 font-extrabold text-band md:self-start'

/** The link that goes with one occasion's lines. A shared occasion belongs to no one person, so it opens who is on it. */
function OccasionLink({ occasion: o, several }: { occasion: Upcoming; several: boolean }) {
  if (o.kind === 'shared' || !o.user) {
    return (
      <a href={occasionPath(o)} className={bandLink}>
        {several ? `See who's on ${o.name}` : "See who's on it"}
      </a>
    )
  }
  return (
    <a href={occasionPath(o)} className={bandLink}>
      See {o.user.name}'s list
    </a>
  )
}

/**
 * The age a birthday turns, from the year of the user's birthday and the year of
 * the birthday's next date. It is null until known, and when the birthday has no
 * year or the user can't be read: the band then says nothing of age. Only a
 * birthday in the band asks, and the band holds the occasions of one day.
 */
function useTurning(o: Upcoming): number | null {
  const [age, setAge] = useState<number | null>(null)
  const userId = o.kind === 'birthday' ? o.user?.id : undefined
  const year = Number(o.next_date.slice(0, 4))
  useEffect(() => {
    if (!userId) return
    let live = true
    getUser(userId)
      .then((u) => live && setAge(u?.birthday.year ? year - u.birthday.year : null))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [userId, year])
  return age
}

/** One occasion's line under the countdown. The Today band is the words alone. */
function OccasionLine({ occasion: o, me, today }: { occasion: Upcoming; me: User; today: boolean }) {
  const turning = useTurning(o)
  const line = turning === null ? occasionName(o, me) : `${occasionName(o, me)}, turning ${turning}`
  if (o.kind === 'shared' || !o.user || today) {
    return (
      <span className="flex flex-col">
        <span className="text-2xl/tight font-semibold">{line}</span>
        {o.kind === 'shared' && o.is_everyone && <span className="text-band-mute">Everyone</span>}
      </span>
    )
  }
  return (
    <span className="flex items-center gap-2.5 text-2xl/tight font-semibold">
      <UserAvatar user={o.user} size={40} />
      <span>{line}</span>
    </span>
  )
}

/** The countdown to the first day with an occasion on it, and every occasion that falls on that day. */
function Next({ occasions, me }: { occasions: Upcoming[]; me: User }) {
  const [first] = occasions as [Upcoming]
  const days = daysUntil(first.next_date)
  const date = <time dateTime={first.next_date}>{longDate(first.next_date)}</time>
  return (
    <div className="mt-3 flex flex-col gap-3.5">
      <div className="flex flex-col gap-2">
        {days === 0 ? (
          <BandText eyebrow={date} big={countdown(days)} large />
        ) : (
          <BandText eyebrow={<>Next up, {date}</>} big={countdown(days)} large />
        )}
        {occasions.map((o) => (
          <OccasionLine key={o.id} occasion={o} me={me} today={days === 0} />
        ))}
      </div>
      {occasions.map((o) => (
        <OccasionLink key={o.id} occasion={o} several={occasions.length > 1} />
      ))}
    </div>
  )
}

/**
 * Home's band: the countdown to the next occasion, from the occasions in the
 * next 60 days (`GET /occasions?next_date_before=`). It never speaks of the
 * user's own birthday or custom occasion. While the occasions load, or when they
 * fail, it says nothing: Coming up shows the failure.
 */
function Band({ loaded, me }: { loaded: Loaded<Occasion>; me: User }) {
  if (loaded.status !== 'ready') return null
  const next = nextOccasions(loaded.rows, me)
  if (next.length > 0) return <Next occasions={next} me={me} />
  return (
    <BandText eyebrow="Next 60 days" big="Nothing coming up">
      No birthdays or occasions.
    </BandText>
  )
}

/**
 * The signed-in shell's front page: a greeting, My lists and Giving, the paste
 * box, People, what is coming up, and Not bought yet. The band shows the next
 * occasion with its countdown. The banner is a one-time line, such as who a login
 * link just signed in.
 */
export function Home({ me, banner }: { me: User; banner?: string | null }) {
  const invited = useInvitationTotal(me)
  const upcoming = useUpcoming()
  return (
    <Screen title="Home" band={<Band loaded={upcoming} me={me} />}>
      {banner && <output className="max-w-xl text-xl font-semibold">{banner}</output>}
      <p className="flex max-w-xl items-center gap-3 text-xl">
        <UserAvatar user={me} size={40} />
        Welcome, {me.name}.
      </p>
      <div className="grid max-w-xl grid-cols-2 gap-2">
        <a href="/lists" className={buttonLink}>
          My lists{invited > 0 && ` · ${invitationCount(invited)}`}
        </a>
        <a href="/giving" className={buttonLink}>
          Giving
        </a>
      </div>
      <PasteBox me={me} />
      <People me={me} />
      <ComingUp loaded={upcoming} me={me} />
      <NotBoughtYet />
    </Screen>
  )
}
