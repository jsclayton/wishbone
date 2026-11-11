import type { ReactNode } from 'react'
import type { User } from './api'
import { BlockedStores } from './BlockedStores'
import { ClaimItem } from './Claim'
import { Deactivate } from './Deactivate'
import { DeleteList } from './DeleteList'
import { DeletePasskey } from './DeletePasskey'
import { AddGroupUser, AddSubgroup, DeleteGroup, GroupPage, Groups, NewGroup } from './Groups'
import { AddAgain, RestoreGift } from './GiftAgain'
import { Given } from './Given'
import { Giving, Unclaim } from './Giving'
import { Home } from './Home'
import { ItemImagesScreen } from './ItemImages'
import { InviteToList } from './InviteToList'
import { ItemPage, DeleteItem } from './ItemPage'
import { LeaveList } from './LeaveList'
import { AddToList, EditList, ListPage, MyLists } from './Lists'
import { Menu } from './Menu'
import { NewList } from './NewList'
import { AddOccasion, EditOccasion, DeleteOccasion } from './Occasions'
import { AddParent } from './Parents'
import { Profile } from './Profile'
import { Received } from './Received'
import { AttachOccasionGroup, DeleteSharedOccasion, EditSharedOccasion, NewSharedOccasion, SharedOccasionPage, SharedOccasions } from './SharedOccasions'
import { SignIn } from './SignIn'
import { UserPage } from './UserPage'
import { NewUser, Users } from './Users'

/** What a screen can use. The table is only for a signed-in user. */
export type RouteContext = {
  /** The signed-in user. */
  me: User
  /** A one-time line for Home, such as who a login link signed in. */
  banner: string | null
  /** Ends the session and opens the signed-out page. */
  onLoggedOut: () => void
  /** Replaces the signed-in user, after they changed something Menu shows, such as their name. */
  onMeChange: (me: User) => void
  /** The groups of a path with a pattern. */
  params: string[]
}

type Route = { path: string | RegExp; screen: (context: RouteContext) => ReactNode }

/**
 * Every signed-in screen. A path that matches nothing opens Home: there are no
 * redirects. A screen whose resource is missing shows the
 * missing-page message (see `Resource`). To add a screen, add one line. A screen
 * brings its own frame (`Screen`).
 */
export const routes: Route[] = [
  { path: '/', screen: (c) => <Home me={c.me} banner={c.banner} /> },
  { path: '/menu', screen: (c) => <Menu me={c.me} onLoggedOut={c.onLoggedOut} /> },
  { path: '/users', screen: (c) => <Users me={c.me} /> },
  { path: '/users/new', screen: (c) => <NewUser me={c.me} /> },
  { path: /^\/users\/([^/]+)$/, screen: (c) => <UserPage key={c.params[0]} userId={c.params[0]!} me={c.me} /> },
  { path: /^\/users\/([^/]+)\/deactivate$/, screen: (c) => <Deactivate key={c.params[0]} userId={c.params[0]!} /> },
  { path: /^\/users\/([^/]+)\/occasions\/new$/, screen: (c) => <AddOccasion key={c.params[0]} userId={c.params[0]!} me={c.me} /> },
  { path: /^\/users\/([^/]+)\/occasions\/([^/]+)$/, screen: (c) => <EditOccasion key={`${c.params[0]}/${c.params[1]}`} userId={c.params[0]!} occasionId={c.params[1]!} /> },
  {
    path: /^\/users\/([^/]+)\/occasions\/([^/]+)\/delete$/,
    screen: (c) => <DeleteOccasion key={`${c.params[0]}/${c.params[1]}`} userId={c.params[0]!} occasionId={c.params[1]!} />,
  },
  { path: /^\/users\/([^/]+)\/parents$/, screen: (c) => <AddParent key={c.params[0]} userId={c.params[0]!} /> },
  { path: /^\/users\/([^/]+)\/profile$/, screen: (c) => <Profile key={c.params[0]} userId={c.params[0]!} me={c.me} onMeChange={c.onMeChange} /> },
  { path: /^\/users\/([^/]+)\/sign-in$/, screen: (c) => <SignIn key={c.params[0]} userId={c.params[0]!} me={c.me} onLoggedOut={c.onLoggedOut} /> },
  { path: '/giving', screen: (c) => <Giving me={c.me} /> },
  { path: '/received', screen: (c) => <Received me={c.me} /> },
  { path: '/given', screen: () => <Given /> },
  { path: /^\/gifts\/([^/]+)\/unclaim$/, screen: (c) => <Unclaim key={c.params[0]} giftId={c.params[0]!} /> },
  { path: /^\/gifts\/([^/]+)\/restore$/, screen: (c) => <RestoreGift key={c.params[0]} giftId={c.params[0]!} /> },
  { path: /^\/gifts\/([^/]+)\/add-again$/, screen: (c) => <AddAgain key={c.params[0]} giftId={c.params[0]!} me={c.me} /> },
  { path: '/lists', screen: (c) => <MyLists me={c.me} /> },
  { path: '/lists/new', screen: (c) => <NewList me={c.me} /> },
  { path: /^\/lists\/([^/]+)$/, screen: (c) => <ListPage key={c.params[0]} listId={c.params[0]!} me={c.me} /> },
  { path: /^\/lists\/([^/]+)\/invite$/, screen: (c) => <InviteToList key={c.params[0]} listId={c.params[0]!} /> },
  {
    path: /^\/lists\/([^/]+)\/recipients\/([^/]+)\/leave$/,
    screen: (c) => <LeaveList key={`${c.params[0]}/${c.params[1]}`} listId={c.params[0]!} userId={c.params[1]!} meId={c.me.id} />,
  },
  { path: /^\/lists\/([^/]+)\/delete$/, screen: (c) => <DeleteList key={c.params[0]} listId={c.params[0]!} /> },
  { path: /^\/lists\/([^/]+)\/edit$/, screen: (c) => <EditList key={c.params[0]} listId={c.params[0]!} /> },
  { path: /^\/lists\/([^/]+)\/add$/, screen: (c) => <AddToList key={c.params[0]} listId={c.params[0]!} me={c.me} /> },
  { path: /^\/items\/([^/]+)$/, screen: (c) => <ItemPage key={c.params[0]} itemId={c.params[0]!} me={c.me} /> },
  { path: /^\/items\/([^/]+)\/claim$/, screen: (c) => <ClaimItem key={c.params[0]} itemId={c.params[0]!} me={c.me} /> },
  { path: /^\/items\/([^/]+)\/images$/, screen: (c) => <ItemImagesScreen key={c.params[0]} itemId={c.params[0]!} /> },
  { path: /^\/items\/([^/]+)\/delete$/, screen: (c) => <DeleteItem key={c.params[0]} itemId={c.params[0]!} /> },
  { path: '/blocked-stores', screen: (c) => <BlockedStores me={c.me} /> },
  { path: '/occasions', screen: (c) => <SharedOccasions me={c.me} /> },
  { path: '/occasions/new', screen: (c) => <NewSharedOccasion me={c.me} /> },
  { path: /^\/occasions\/([^/]+)$/, screen: (c) => <SharedOccasionPage key={c.params[0]} occasionId={c.params[0]!} me={c.me} /> },
  { path: /^\/occasions\/([^/]+)\/edit$/, screen: (c) => <EditSharedOccasion key={c.params[0]} occasionId={c.params[0]!} /> },
  { path: /^\/occasions\/([^/]+)\/delete$/, screen: (c) => <DeleteSharedOccasion key={c.params[0]} occasionId={c.params[0]!} /> },
  { path: /^\/occasions\/([^/]+)\/groups$/, screen: (c) => <AttachOccasionGroup key={c.params[0]} occasionId={c.params[0]!} /> },
  { path: '/groups', screen: (c) => <Groups me={c.me} /> },
  { path: '/groups/new', screen: (c) => <NewGroup me={c.me} /> },
  { path: /^\/groups\/([^/]+)$/, screen: (c) => <GroupPage key={c.params[0]} groupId={c.params[0]!} me={c.me} /> },
  { path: /^\/groups\/([^/]+)\/users$/, screen: (c) => <AddGroupUser key={c.params[0]} groupId={c.params[0]!} me={c.me} /> },
  { path: /^\/groups\/([^/]+)\/groups$/, screen: (c) => <AddSubgroup key={c.params[0]} groupId={c.params[0]!} /> },
  { path: /^\/groups\/([^/]+)\/delete$/, screen: (c) => <DeleteGroup key={c.params[0]} groupId={c.params[0]!} /> },
  {
    path: /^\/users\/([^/]+)\/passkeys\/([^/]+)\/delete$/,
    screen: (c) => <DeletePasskey key={`${c.params[0]}/${c.params[1]}`} userId={c.params[0]!} passkeyId={c.params[1]!} me={c.me} />,
  },
]

/** The route for a path, with the groups its pattern captured. */
export function findRoute(path: string) {
  for (const route of routes) {
    if (route.path === path) return { route, params: [] }
    if (route.path instanceof RegExp) {
      const match = route.path.exec(path)
      if (match) return { route, params: match.slice(1) }
    }
  }
  return null
}
