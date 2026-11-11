# Wishbone Specification

This spec says what Wishbone does, and it is the source of truth. Terms in **bold** are defined in the [glossary](../GLOSSARY.md). Key trade-offs are recorded in [`docs/adr/`](adr/). Every route, with the roles that may call it and the fields each role sees, is in the [route table](routes.md).

## Goals

- A family wish list app that only the person who runs it has to set up. Everyone else gets in by tapping a link or scanning a QR code.
- Adding an item is "paste any link". Wishbone works out the title, price, and image.
- Gifts never spoil the surprise, and lists clean themselves up after each occasion.
- Simple, lightweight, and fast. It runs as one Docker container in a homelab.

## Non-goals

- Public or multi-tenant hosting. Self-registration.
- Email, push, or SMS of any kind.
- OIDC or other external sign-in providers.

## Users and parents

### Users

- Everyone in the instance is a **user**. The **admin** is also a user. See [Admin and setup](#admin-and-setup).
- A user can have no sessions and no passkeys. That user never signs in, and nothing else changes. For example, a toddler's parent edits the toddler's lists, and later makes the toddler a login link. There is no special state or flag for a user who can't sign in.
- A user's profile has a name (required), an optional last name, an optional avatar, a birthday, a timezone, and **good to know**. Every user has a birthday (see [Occasions of a user](#occasions-of-a-user)).
- The web app shows a user's name followed by their last name where it lists users and on their page. A summary has only the name. Users sort by name, not by last name.
- A user with no avatar shows initials: the first letter of the name and of the last name, or only the first letter of the name when there is no last name. A summary has no last name, so it gives one letter.
- A birthday has a month and a day, and it may have a year. When it has a year, Home's band shows the age the user is turning. A February 29 birthday falls on February 28 in a year that isn't a leap year. A user can change their own birthday.
- Good to know is one markdown field that givers can see, for example sizes, favorite colors, allergies, or "no more stuffed animals".
- A user's timezone is read from the browser and updated silently. When the web app loads for a signed-in user, and the browser's timezone is not the user's `timezone`, the app sets it with `PATCH /users/me`. It shows nothing and doesn't wait: a failed report changes nothing, and the next load tries again. It sets only the signed-in user's own timezone, never a child's. A user whose browser has never reported a timezone has none, and uses the server's `TZ` (see [Configuration](#configuration)): the zone for their reveal moment and for the `next_date` of their occasions. The `next_date` of a shared occasion, which has no user, is worked out in the caller's zone, and so uses `TZ` for a caller who has none. With no `TZ`, that zone is UTC. The API refuses a name that is not an IANA zone.
- Good to know and the avatar describe the user, so they are on the user and not on a list. A user with several lists has one good to know and one avatar. Text about one list goes in that list's **description** (see [Description](#description)).

### Parents and children

- The admin can link one user as the **parent** of another user. That other user is then the parent's **child**.
- Parent and child is the only relationship between users. There is no partner relationship. Adults manage their own content, and two users who share a list do so by consent.
- A child can have several parents, and a parent can have several children.
- Only the admin links and unlinks a parent and a child.
- The admin can't have parents. Wishbone refuses to link a parent to the admin, and refuses to make a user with parents the admin.
- Wishbone refuses a link that would make a loop. For example, if Grandma is Dad's parent, Dad can't be Grandma's parent. A user can't be their own parent.
- For each of their children, a parent can:
  - make login links (each one expires after 7 days)
  - see and revoke the child's sessions
  - see and delete the child's passkeys (only the user adds and renames a passkey)
  - edit the child's profile
  - add, edit, and delete the child's custom occasions
  - see all of the child's lists
  - edit all of the child's lists
- While the child is deactivated, these powers stop. The parent still sees the child and their lists. See [Who acts while a user is deactivated](#who-acts-while-a-user-is-deactivated).
- A parent's powers apply only to their direct children. If Grandma is Dad's parent and Dad is Emma's parent, Grandma has no powers over Emma. To give Grandma those powers, the admin links Grandma as Emma's parent.
- A parent's powers don't include the gifts that the child gives. A parent can't claim in the child's name, and can't see the child's Giving or Given. A gift always belongs to the signed-in user who claimed the item. A parent does see the gifts given to the child (see [Who sees a gift](#who-sees-a-gift)).
- Anyone who can see a user also sees that user's parents and children, as summaries. See [Visibility](#visibility).
- When the admin unlinks a parent and a child, the parent loses their powers at once. Login links the parent made earlier work until they are used or expire. The child's sessions and passkeys don't change.
- Accepted leak: a parent can make a login link for their child and sign in as the child. Givers don't see fully claimed items. So, as the child, the parent can see which items are missing from their own list, and how many of an item are still wanted. They can also see what the child is getting them. They can't see who else claimed what.

### Screens

- **People** on Home shows these users, from top to bottom:
  1. **Your children**: the user's children, when they have any.
  2. **Your parents**: the user's parents, when they have any.
  3. The user's groups: each group's users, and "through {group}" for a group that the user is in by nesting.
- Each part lists its users by name. A child isn't listed under their parent in a group. A user shows once in each part that has them, so a child who shares a group with their parent shows in Your children and in that group.
- Your children and Your parents show the user's `children` and `parents` who are in `GET /users`. They show whether or not the user shares a group with them. So a parent always finds their child here, and a deactivated parent doesn't show to their child.
- The groups part shows only the groups that the user is in (`GET /groups?includes_me=true`). So the admin's People shows only the groups that the admin is in, as for any other user: Home is about giving, and the admin manages users from the Users screen (see [Admin and setup](#admin-and-setup)).
- The heading stays "People" as UI copy, as "Add to my list" does. "People" is only the section's name: code, the API, and docs don't use "people" or "person" for users.
- People doesn't mark a user who never signs in. Whether a user signs in doesn't change how anyone gives to them.
- Each user in People opens their user page (see [Lists](#lists)).
- A user's page has a **For {name}** part below the lists, for example "For Emma". It has these buttons, and each one shows from its `can_*` field. When none of them shows, the part doesn't show.
  - **Edit {name}'s profile** (`can_edit`), for example "Edit Emma's profile": opens the user's profile screen.
  - **Sign {name} in** (`can_make_login_link` or `can_manage_sessions`), for example "Sign Emma in": opens the user's sign-in screen.
- A parent gets the For part on each child's page, and the admin gets it on every user's page, with more buttons (see [Admin and setup](#admin-and-setup)). Menu has no screen for a user's children. People and the My lists screen already lead to each child.
- The **profile screen** (`/users/{id}/profile`) is the same for the user and for anyone who acts for them. It has the user's name, last name, avatar, birthday, and good to know, and the user's occasions (see [Occasions](#occasions)). It has no timezone field, because the browser reports it. For a child, edits don't set `profile_confirmed_at`: only the user confirms their own profile.
- The **sign-in screen** (`/users/{id}/sign-in`) shows:
  - **Make a login link**, from `can_make_login_link`. The link shows as a QR code first, then a link to copy.
  - The user's sessions, from `GET /users/{id}/sessions`, each with **Revoke**, from the session's `can_delete`. With no sessions, it says "Not signed in on any device".
  - The user's passkeys, from `GET /users/{id}/passkeys`, each with **Delete**, from the passkey's `can_delete`. Only the user gets **Rename** (the passkey's `can_edit`). **Add a passkey** shows only on the user's own sign-in screen. So a parent can remove the passkey on a child's lost device, as well as its session.
  - Deleting the user's last passkey opens a confirmation screen. For the user, it says "You'll need a new link or QR code to get back in." A user with no parents is also told that only the admin can make them a new link, and the admin is told that only the person who runs Wishbone can. For anyone else, it says "{name} will need a new link or QR code to sign in on a new device."
- Menu's "Your profile" opens `/users/{id}/profile` with the user's own ID, and "Your devices" opens `/users/{id}/sign-in` with the user's own ID. The web app doesn't use `me` in its paths. The user's own user page still opens the My lists screen.
- Only the admin's screens link and unlink a parent and a child (see [Admin and setup](#admin-and-setup)).

## Admin and setup

### The admin

- One user is the **admin**. The first user is the admin. After setup, there is always exactly one admin. See [Setup](#setup).
- Admin is a flag on the user. It doesn't depend on how the user signs in. A passkey is not required.
- Only the CLI changes which user is the admin, with `wishbone set-admin`. A user's `is_admin` field is read-only in the API. No route changes it.
- The admin can't have parents. See [Parents and children](#parents-and-children).
- For any user, the admin can:
  - make login links (each one expires after 7 days)
  - see and revoke the user's sessions
  - see and delete the user's passkeys
  - edit the user's profile
  - deactivate and reactivate the user
  - link and unlink the user's parents and children
- While a user is deactivated, the admin can only reactivate them, change their parent links, or delete them with the CLI. See [Deactivation](#deactivation).
- The admin sees every user and every group, so they can manage them.
- Being the admin gives no other access to lists, custom occasions, or gifts. The admin sees a list only through the same visibility rules as every other user. To edit a child's lists, the admin links themselves as the child's parent. They are then a parent like any other.
- Nobody can deactivate the admin. To deactivate the user who is the admin, the operator first moves the role to another user with `wishbone set-admin`.
- Accepted leak: the admin can make a login link for any user and sign in as that user. `wishbone login-link` has the same power, and the spec accepts the same leak for parents.
- Accepted leak: the admin can link themselves as the parent of any user who isn't the admin, and then see and edit that user's lists. This gives no more than a login link does, and it's easier to notice, because anyone who can see a user sees their parents.

### Users and groups

- Only the admin creates users. To create a user, the admin enters a name and a birthday. The birthday's month and day are required, and its year is optional. The last name and the user's parents are optional.
- A user's `profile_confirmed_at` is `null` until they confirm their profile.
- Every user has a birthday. Nobody can clear it: a `PATCH` that sets `birthday` to `null` is a 422. So every user always has an upcoming occasion (see [Occasions](#occasions)).
- Only the admin creates, renames, and deletes groups, and adds users to them and removes users from them. A group has no owners.
- The admin also nests groups in other groups. What a group grants is in [Visibility](#visibility).

### Server-wide settings

- In the app, only the admin manages these:
  - users, parent links, and deactivation
  - groups
  - blocked stores
  - shared occasions: the admin can create, edit, and delete any shared occasion. A shared occasion reaches the groups it is attached to, or everyone. See [Shared occasions](#shared-occasions).
- These are not in the app:
  - The admin flag. The CLI sets it.
  - Extraction, search, rate limits, and other configuration. They are environment variables. No route reads or writes them, so no secret goes into the database or the API.
  - Backups and health checks. They stay CLI commands.

### Setup

- While the instance has no users, each server start makes a one-time **setup code** and writes it to the log. A setup code works until it is used, or until the server starts again. Wishbone stores setup codes hashed.
- `GET /setup` needs no sign-in. It returns `{ "is_needed": true }` while the instance has no users, and `{ "is_needed": false }` after.
- `POST /setup` needs no sign-in. It takes `setup_code`, `name`, `birthday` (a partial date), and an optional `last_name`. It creates the first user as the admin, and starts a session for the caller. As every create does, it returns 201, a `Location` header with the user's address, `/users/{id}`, and the full user.
  - The session starts in the same way as when a login link is redeemed with a cookie. Setup gives no bearer token: the admin runs setup in the web app, and a native app signs in later with a login link (see [Sign-in](#sign-in)).
  - A wrong setup code is a 422 with `code: "setup_code_invalid"`.
  - Setup attempts count against the same per-IP limit as login link redemption (`WISHBONE_RATE_LIMIT_REDEEM`). Over the limit, the route returns 429 with `code: "rate_limited"`.
- When the instance has a user, `POST /setup` returns 409 with `code: "setup_complete"`, and creates nothing. Setup can't run again, because the admin can't be deleted.

### CLI

| Command | What it does |
|---|---|
| `serve` | Starts the web server. Before it applies migrations, it runs the version guard and the automatic backup (see [Migration](#migration)). |
| `users` | Prints one line for each user: the ID, the name, the last name if there is one, and `admin` or `deactivated` where it applies. It prints no other profile fields. |
| `login-link <user>` | Prints a login link for any user, including the admin, at any time. The link expires after 7 days. This is the recovery path for everyone, including the first admin. Refuses a deactivated user. |
| `set-admin <user>` | Makes the user the admin. The old admin becomes a normal user and keeps their sessions. Refuses a user who has parents, or who is deactivated. |
| `delete-user <user>` | Hard-deletes a user. It can't be undone. Refuses to delete the admin. It prints each child left with no parent. What a delete removes and keeps is in [Deletion](#deletion). |
| `backup <path>` | Writes a consistent snapshot (see [Backups](#backups)). |
| `healthcheck` | Exits 0 when the local server is healthy, by calling `/api/v2/health`. For a container's health check. |
| `version` | Prints version information. |

- `<user>` is the user's ID. The commands don't accept names. `wishbone users` shows the IDs.
- Only `serve` applies migrations. Every command except `backup` refuses when a migration is pending (see [Start order](#start-order)).

### Screens

- No screen changes a user's `is_admin` field.
- Until a user's `profile_confirmed_at` is set, every address in the web app opens the "finish your profile" steps:
  1. The name, an optional last name, and the birthday, starting with what is there, so the user fills in anything missing.
  2. "Add a photo" asks for an avatar. The user can skip it.
  - The profile is confirmed when the user finishes the second step. The steps have no top row, and each one is its own history entry, so the browser's Back goes to the step before. Log out stays on the first step.
- The web app has no settings screens.
- The web app shows a setup screen when `is_needed` is `true`. The operator enters the setup code from the log, and the first user's name and birthday.
- Menu has a **For the admin** part, only for the admin, with **Users**, **Shared occasions**, and **Blocked stores**. Groups is in everyone's part of Menu.
- The **Users** screen (`/users`) shows every user, from `GET /users`, by name. It shows "Deactivated" beside a deactivated user (see [Deactivation and deletion](#deactivation-and-deletion)). Each user opens their user page.
- The Users screen has a **New user** button. Its form asks for a name, a birthday, an optional last name, and optional parents. After the admin saves, the new user's page opens.
- On a user's page, the admin's For part (see [Users and parents](#users-and-parents)) adds:
  - **Parents**, from `can_edit_parents`: the user's parents, each with **Remove**, and **Add parent**, which lists every other user who isn't already their parent, by name. The list doesn't leave out users that would make a loop. Wishbone refuses a link that would make a loop, and the screen shows the error's `detail`. Neither one opens a confirmation screen, because each is easy to undo.
  - **Deactivate {name}**, from `can_deactivate`, which opens a confirmation screen, or **Reactivate {name}**, from `can_reactivate`, which doesn't, because reactivation brings the user back as they were.
- For a user whom the admin sees only through the admin's access, the user page shows none of the primary list's items, because `primary_list` is hidden from that access. Other lists shows only the extra lists that the admin can see through another recipient. The rest of the page is as usual.

## Access

### Instance exposure

- The instance is reachable from the public internet behind a reverse proxy.
- A browser without a session sees a bare page with a "Sign in with passkey" button and a line that says to open a login link. The page shows no user names, lists, or other household details.

### Login links

- A login link works once.
- Opening a login link does not redeem it. The page shows a "Sign in" button, and only the tap redeems the link. Messaging apps load links to build previews, and a preview must not use up the link. The page shows when the link expires, as a date and time, above the button.
- The page checks a link before it redeems it (`POST /login-links/check`). The check has a rate limit of its own, of the same size as the redeem limit, so a check doesn't use up the attempts of a person who is signing in.
- Redeeming a link starts a **session** that lasts 365 days after its last use. Each use moves that date forward. Redemption is rate-limited per client IP (`WISHBONE_RATE_LIMIT_REDEEM`).
- If a browser already has a session when it opens a link for a different user, the page warns that the current user will be signed out on that browser, and offers to stay signed in. If the person goes on, the server revokes the current session, the browser signs in as the link's user, and a one-time banner says "You're now signed in as {name}."
- A login link of a deactivated user doesn't work (see [Deactivation](#deactivation)).

### Sessions and passkeys

- A browser holds at most one session. There is no account switching.
- A user sees their own sessions (the device or user agent, and when it was last used) and can revoke them. A parent can do the same for a child, and the admin for any user (see [Parents and children](#parents-and-children) and [The admin](#the-admin)).
- Any user can add passkeys. A signed-out browser can sign in with a passkey.
- Wishbone never collects email addresses.

## Lists

### Recipients

- A **list** is a set of items. Its **recipients** are the users that the gifts are for.
- A list has one or more recipients. Recipients are equal. No recipient has more say over the list than another.
- Recipients don't see the claim state of their list. Every other user who can see the list sees its claim state as a giver does. This includes a parent of a recipient, when the parent isn't a recipient.
- Who can see a list is in [Visibility](#visibility).

### Who edits a list

- These users can edit a list: its recipients, and the parents of each recipient. To edit a list is to add, change, reorder, and remove its items, to rename it, and to change its description.
- A parent who edits an item that someone claimed causes the same change flags for the claimer as a recipient's edit does.
- Recipient is the only role that a user can have on a list. There is no editor who isn't a recipient. Another role, such as that editor, can be added later as an additive change.

### Primary list

- Every user has exactly one **primary list**. Wishbone creates it with the user, so a user always has one.
- A primary list has exactly one recipient: its user. Nobody can invite another recipient to it.
- Nobody can delete a primary list.
- A user's `primary_list` field names their primary list. A client that has no list to name, for example a share sheet, adds the item to that list.
- A primary list's name is the user's name, and it changes when the user's name changes. After a user who can edit the list renames it, it keeps its own name.

### Extra lists

- Any user can create an **extra list**. They are its first recipient.
- A parent can create an extra list for their child. The child is its first recipient, and the parent is not a recipient.
- An extra list has a name. Wishbone requires it.

### Description

- A list has an optional **description**: markdown text about that list. For example, "Our house" (Mom and Dad) has "We're redoing the kitchen in sage green. Nothing for the garage."
- Anyone who can see the list sees its description. The users who edit the list change it (see [Who edits a list](#who-edits-a-list)).
- A primary list has a description too. It starts with no description, and it doesn't follow the user's good to know.
- A list has no image of its own. An image on a list can be added later as an additive change.

### Invitations

- A recipient of an extra list can invite a user who they can see to become a recipient. The admin's access to every user doesn't count here, as in [Who can see a list](#who-can-see-a-list). The **invitation** stays pending until the invited user accepts or declines it.
- The invited user, or a parent of the invited user, accepts or declines.
- Until the invited user accepts, nothing changes for them. They see the list, and its claim state, as before.
- A recipient of the list can withdraw an invitation that is still pending.
- A pending invitation is visible to the invited user, their parents, and the list's recipients. For everyone else it is a 404.

### Leaving and deleting

- A recipient can leave an extra list while another recipient remains. A parent can make their child leave.
- The last recipient of an extra list can't leave. They delete the list instead.
- A recipient can delete an extra list only when they are its only recipient. A parent can delete their child's extra list when the child is its only recipient.
- Deleting an extra list deletes each of its items, as a delete by a user who can edit the list does (see [Who changes an item](#who-changes-an-item)). Items with open gifts are soft-deleted: each open gift is flagged "removed", and its giver keeps or drops it. A kept gift is given at reveal to the list's last recipients. Wishbone keeps what kept gifts need for their reveal, and nobody sees the list again.
- Deleting a list never refuses because of its gifts. A refusal would show a recipient who deletes it whether its items are claimed.
- Accepted leak: a user who leaves a list and can still see it is then a giver, so they see its claim state. That includes gifts already claimed for them.

### Screens

- **Home** is about giving (see [Home](#home)). It shows no lists of its own.
- Beside Giving, a **My lists** button opens the My lists screen, also for a user who has only their primary list. While invitations for the user or their children are pending, the button shows how many, for example "My lists · 1 invitation".
- The **My lists** screen shows every list that the user can edit (`GET /lists?can_edit=true`). From top to bottom:
  1. **Invitations**: the pending invitations for the user and for their children, for example "Mom invited you to Our house" or "Mom invited Emma to Cousins". Each has Accept and Decline. This part shows only when invitations are pending.
  2. **The user's own lists**: their primary list first, then the extra lists that they are a recipient of, by name.
  3. **One part for each child**, under the child's name: the child's primary list first, then the child's extra lists, by name. A list that has both the user and their child as recipients shows only once, under the user's own lists.
     - A deactivated child's part still shows, with "Deactivated" beside the child's name. Its lists come from `GET /lists?recipient_id={child}`, because a list that the user can edit only through the child isn't in `?can_edit=true` while the child is deactivated.
  4. One **New list** button.
- Accept and Decline don't open a confirmation screen, because both are easy to undo: a recipient can invite the user again, and the user can leave the list. After an accept, the list shows in the user's own lists, or in the child's part.
- Each list on the My lists screen shows its name, its recipients' avatars, and its `item_count`. It opens the list's page.
- The New list form asks for a name and an optional description. If the user has children, it also asks who the list is for: the user (the default), or one of their children. For a child, the child is the first recipient, and the user isn't a recipient (see [Extra lists](#extra-lists)). After the user saves, the new list's page opens.
- The People section on Home shows the user's children, their parents, and their groups' users. Each user opens their user page. See [Users and parents](#users-and-parents).
- A **user's page** shows the items of that user's primary list first. Below them, **Other lists** shows the extra lists that the user is a recipient of, by name, with the same rows as the My lists screen. When the user has no extra lists, Other lists doesn't show.
- A list with several recipients shows on the page of each recipient. For example, "Our house" shows on Mom's page and on Dad's page.
- The user's own user page opens the My lists screen.
- A list shows its recipients' avatars where it would show an image. For example, "Our house" shows Mom's and Dad's avatars.
- A **list's page** (`/lists/{id}`) shows, from top to bottom:
  1. A pending invitation to this list for the user or for one of their children, as on the My lists screen. For example, "Mom invited you to this list" or "Mom invited Emma to this list". **Accept** shows from the invitation's `can_accept`, and **Decline** from its `can_delete`. So while Emma is deactivated, her parent sees her invitation but can't accept it.
  2. The list's name, and its recipients' names and avatars.
  3. The list's description, as markdown, when it has one. When `can_edit` is `true`, an **Edit list** button changes the name and the description.
  4. For a user who can edit the list and is also a giver, one line: "You see what's been claimed here. Fully claimed items don't show." This is a parent who isn't a recipient. The line tells them why a fully claimed item is gone, so they don't add it again.
  5. The items, in position order, and, when `can_edit` is `true`, an **Add** button that adds to this list.
  6. For a recipient: the list's other pending invitations, and an **Invite** button. An invitation that part 1 already shows isn't shown again here.
  7. **Leave**, **Make {child} leave**, and **Delete list**, as their `can_*` fields allow.
- A primary list's page is the same as the page of any other list. A user's page still shows the items of their primary list itself (above).
- On a user's page, the primary list's items follow the same rules as on a list's page: the controls show from the `can_*` fields. So on a child's page, a parent who isn't a recipient sees both the edit controls and the claim state.
- A list's page shows no recipient's good to know. Good to know shows only on a user's page. Text about the list is its description.
- A recipient's name opens their user page only when the user can see that recipient, which the web app knows from `GET /users`. Every other recipient shows their name and avatar with no link, and nothing says why. This includes a deactivated recipient, for each user who can't see them.
- A user who gets a recipient's `deactivated_at` from `GET /users` (the recipient's parents and the admin) sees "Deactivated" beside the recipient's name.
- A list's page and an item's page show their controls from the `can_*` fields, not from who the user is:
  - The user is a recipient when they are in the list's `recipients`. Otherwise they are a giver.
  - The edit controls (add, edit, reorder, delete, refresh prices, and Edit list) show when `can_edit` is `true`.
  - The claim state shows for a giver: how many are still wanted, and the user's own gifts. **Claim** shows when `can_claim` is `true`.
  - So a parent who isn't a recipient sees both. A recipient sees only the edit controls, and a giver who can't edit sees only the claim controls.
  - While a recipient is deactivated, `can_edit` through them is `false` for their parents, so the edit controls don't show (see [Who acts while a user is deactivated](#who-acts-while-a-user-is-deactivated)).
- Each item on a list's page shows its primary image, its title, and its price. For a giver, it also shows how many are still wanted: "3 of 5 still wanted", or "Always wanted" for an unlimited item. When `my_claimed_quantity` is above 0, it also shows "You claimed 2". A recipient sees neither.
- **Invite** shows when `can_invite` is `true`. It opens a list of the users that the user can invite (`GET /users?invitable_to={list_id}`), with their names and avatars, by name. A tap on a user invites them, with no confirmation, because a recipient can withdraw the invitation.
- For a recipient, the list's page shows the other pending invitations to the list (`GET /invitations?list_id={id}`, without the one that part 1 shows), only while one is pending. Each shows the invited user's name and avatar, and who invited them, for example "Grandpa · invited by Mom". **Withdraw** shows from the invitation's `can_delete`, with no confirmation.
- On the My lists screen, a tap on an invitation's text opens the list's page, when the user can see the list. The user can accept or decline there too. After an accept, the list's page shows the list as its recipients see it, with no claim state.
- **Leave** shows when the list's `can_leave` is `true`. For a parent, **Make Emma leave** shows for each child of theirs whose row in `GET /lists/{id}/recipients` has `can_delete` set to `true`. Each opens a confirmation screen whose button repeats the action, for example "Leave Our house" or "Make Emma leave Our house". After the leave, the My lists screen opens.
- **Delete list** shows when `can_delete` is `true`. It opens a confirmation screen whose button repeats the action, for example "Delete Cousins and its items". The words never depend on the list's gifts. After the delete, the My lists screen opens.
- Before a user leaves an extra list that they can still see after they leave, the app warns them: "You'll see what's been claimed on this list, including gifts for you." The confirmation screen shows the warning when the user's row in `GET /lists/{id}/recipients` has `sees_list_after_leaving` set to `true`.
- When a parent makes their child leave an extra list that the child can still see after they leave, the parent sees a warning about the child: "{name} will see what's been claimed on this list, including gifts for {name}." For example, "Emma will see what's been claimed on this list, including gifts for Emma." The child's row gives `sees_list_after_leaving`.
- The web app never warns about gifts before a list is deleted. A warning would show a recipient who deletes it whether its items are claimed.

## Visibility

### Groups

- A **group** contains users and other groups. Groups can nest to any depth. Wishbone refuses a change that would make a loop.
- Two users share a group when some group contains both of them, directly or through nesting.
- A user sees which groups they belong to. The admin sees every group.
- Deleting a group doesn't affect its users or the groups nested in it. It removes only the group and its nesting links.

### Who can see a user

- User A can see user B when one of these is true:
  - A is B.
  - A and B share a group.
  - A is B's parent, or B is A's parent.
  - A is the admin.
- A deactivated user is the exception. Only the admin, their parents, and the user see them. See [Deactivation](#deactivation).
- A user who A can see appears in `GET /users`, and `GET /users/{id}` returns them.
- Some resources contain a summary of a user: a list's recipients, and the parents and children of a user. A user summary is `{ id, name, avatar }` (see [Shapes](#shapes)).
- A summary doesn't make a user visible. If A can't see B by the rules above, A sees only B's summary, and `GET /users/{id}` for B is a 404 for A. A can still load the avatar in B's summary (see [Images](#images)).

### Who can see a list

- User A can see a list when A is one of its recipients, or when A can see one of its recipients. The admin's access to every user doesn't count here.
- This rule is the same for a primary list and for an extra list. A list has no visibility setting of its own.
- A user who can see a list sees all of its items. An item has no visibility setting of its own.
- These follow from the rule:
  - A parent sees all of their child's lists, and a child sees all of their parents' lists.
  - A list with several recipients is seen by everyone who can see any one of them. For example, Mom's sister sees Mom and Dad's list, and Dad's summary on it, even when she shares no group with Dad.
  - A user who leaves a list still sees it only if the rule lets them. Then they see its claim state, as [Leaving and deleting](#leaving-and-deleting) says.
  - The admin sees a list only as any other user does.

Example:

```
In-laws = { My family, Larkin family, Mother-in-law }
Hollis = { My family, Quill family, Rowan's family, Parents }
```

- The Larkin family sees the lists of My family and of Mother-in-law. It doesn't see a list whose recipients are all in the Quill family, Rowan's family, or Parents.
- My family sees everyone's lists in both groups.
- A list shared by someone in the Larkin family and someone in the Quill family is seen by both groups. Each side sees the other side's recipient only as a summary.

## Occasions

### Occasions of a user

- A user's occasions come from three sources:
  - **Birthday**: from the profile. Every user has one, so every user always has an upcoming occasion.
  - **Shared occasions**: see below.
  - **Custom occasions**: defined for one user by that user or their parent (see [Parents and children](#parents-and-children)). Anyone who can see the user sees their custom occasions, but the admin's access doesn't count.
- An occasion uses one of four date rules:
  - Annual fixed date (Dec 25).
  - One-time date (2027-05-14).
  - Annual nth weekday of a month (2nd Sunday of May, 4th Thursday of November).
  - Annual last weekday of a month (last Monday of May).
- A holiday on a lunar or computed calendar is a one-time occasion, added again each year. A user, or a parent for their child, adds it as a custom occasion. The admin adds it as a shared occasion.
- An annual date of February 29 falls on February 28 in a year that isn't a leap year.
- A birthday is an occasion with its own ID, as shared and custom occasions are. Wishbone creates it with the user, and it goes only when the user is deleted. Nobody edits or deletes it: its date follows the user's birthday, and its name is always "Birthday". So a gift names its occasion by ID, whatever the kind.

### Shared occasions

- Only the admin creates, edits, and deletes shared occasions (see [Server-wide settings](#server-wide-settings)).
- Only the admin attaches groups to a shared occasion and detaches them.
- A shared occasion reaches users in one of two ways:
  - **Attached to groups.** It applies to every user in those groups, directly or through nesting. A user who is in none of those groups doesn't get it.
  - **Everyone.** It applies to every user in the instance, including users in no group and users created later.
- The admin can mark a shared occasion everyone, or unmark it, as an edit. Marking it clears its attached groups. While it is marked, the admin can't attach a group to it. Unmarking it leaves it attached to no groups, and the admin can then attach groups.
- A shared occasion has no creator.

### Screens

- A user's occasions show on their profile screen, for the user and for each parent who opens it (see [Users and parents](#users-and-parents)). The admin sees only the occasions that `GET /occasions` gives them, so no custom occasions through the admin's access. **Add** shows from the user's `can_add_occasion`, and edit and delete show from each occasion's `can_*` fields.
- Adding, editing, and deleting a custom occasion each have their own screen: `/users/{id}/occasions/new`, `/users/{id}/occasions/{occasion_id}`, and `/users/{id}/occasions/{occasion_id}/delete`.

## Items

### Items

- An **item** is one thing that a list's recipients want. It is on exactly one list.
- Item fields:
  - Title (required)
  - **Store links**: zero to 10 (see [Store links](#store-links))
  - Images: up to 5, and a **primary image** (see [Images on an item](#images-on-an-item))
  - Item price, with a currency code, and an optional top price that makes it a range
  - Notes
  - Quantity, or **unlimited**
  - Position: the order that the users who can edit the list set
  - `added_by`: the user who added it
- A list's items come in position order by default. A caller can also sort them by date or price with `?sort=` (see [Collections](#collections)).
- Position is the only way to say "this first". A **Change order** button on a list reorders its items without dragging.
- Wishbone captures a store link's price when the link is added or refreshed. It doesn't track prices in the background.
- Each price is stored with the currency that the search found, and shown with no conversion. For a price typed by hand, the default currency is USD.
- An item can be free text ("a book about sharks"). Its title and notes are the text as typed (see [Search](#search)). Notes hold at most 2000 characters. If the text is longer, the form keeps the first 2000 and says so.

### Who changes an item

- A user who can edit the list (see [Who edits a list](#who-edits-a-list)) can change, reorder, or delete any item on it, whoever added it. An item has no owner, as a list has none.
- A user who can't edit a list can't add an item to it.
- `added_by` is a user summary. It is set when the item is added, and it never changes. If that user is deleted, it becomes `null`. Everyone who can see the item gets it from the API.
- When a user who can edit the list changes an item that someone claimed, the gift is flagged (see [Changes while a gift is open](#changes-while-a-gift-is-open)).
- When a user who can edit the list deletes an item that has open gifts, the delete is soft. Givers stop seeing the item. Each open gift is flagged "removed", and its giver keeps it (it is given at reveal as normal) or drops it.

### Store links

- A **store link** is one place where an item can be bought: a product page at a store, with its own price, currency, and captured date. It also has a store (from the URL's domain) and an optional image URL.
- Store links are interchangeable ways to buy the same product, so an item with several store links is one item, not several. A different color, size, or edition is a separate item. Wishbone doesn't match products across stores on its own, because a wrong merge is worse than a missed one. See [ADR 0005](adr/0005-store-links-sit-under-an-item.md).
- An item has `store_links`, an array of at most 10.
- A user who can edit the list adds a store link from the item (**Add link**). Search reads the URL and fills in the store and the price. A store link that is added later doesn't change the item's title or images.
- That user also removes store links and edits their prices. A refresh changes only the store links' prices, never the item price. A store link whose refresh finds no price keeps its old price. Each store link shows when its price was captured.
- That user can set, change, or clear a store link's price and currency by hand, whatever the search found. This covers a link whose page had no price. The captured date becomes the time of the edit. A later refresh replaces a typed price with the one it finds.
- Givers see the store links sorted by price, lowest first, within one currency. Store links with no price go last, in the order they were added. If the store links use different currencies, they show in the order they were added. A user who can edit the list sees them in the order added.
- The item price is the price givers see on the item. Each store link also shows its own price. The item price starts as the price and currency of the item's first priced store link, and is set from a store link once. A user who can edit the list can set, change, or clear the item price and its top price ("up to") on any item, with or without store links. The item and its store links can use different currencies. Each shows its own, with no conversion. [The item price](routes.md#the-item-price) in the route table has the rules.
- When the last store link is removed, the item stays, with its title and its own price or range.
- A gift is of the item, not of a store link. Wishbone doesn't record which store the giver used.

### Images on an item

- An item has `images`, an ordered array of at most 5 image summaries, in the order they were added.
- An item has `primary_image`, its main image summary. It is always one of `images`. It is `null` when the item has no images.
- A user who can edit the list sets the images with `image_ids`, which replaces the whole array, and picks the primary image with `primary_image_id`. For example, `PATCH /items/{id}` with `{"image_ids": ["31", "40", "52"], "primary_image_id": "40"}`. An image left out of `image_ids` is removed, and the server deletes it (see [Images](#images)).
- Wishbone refuses more than 5 images, and a `primary_image_id` that isn't one of the item's images, with a 422. An image that is attached to a different resource is a 409.
- The first image added to an item becomes its primary image. When the primary image is removed, the first remaining image becomes the primary image, or `null` if none remain.
- A change of the primary image doesn't change the order of `images`. `image_ids` accepts any order, so the API already supports reordering images.

### Moving an item

- A user who can edit both lists can move an item from one to the other, with `PATCH /items/{id}` and a new `list_id`. For example, Dad can move the grill from "Our house" (Mom and Dad) to his primary list. Mom can't, because she can't edit Dad's primary list.
- The item's open gifts follow it. Each one is flagged "moved to {list}". The rules for an occasion that stops applying, and for a giver who can no longer see the list, apply to the new list (see [Changes while a gift is open](#changes-while-a-gift-is-open)).
- Accepted leak: after the move, a user who was a recipient of the item on the old list, and who can see the new list, is a giver of it. They see whether it is claimed, including gifts that were for them. In the example, Mom sees whether the grill is claimed. The move is never refused, because that would show a recipient who moves the item whether it is claimed.

### Duplicates

- The server doesn't detect duplicate items. An item whose store link is already on a list is added as a second item.

### Screens

- The paste box on Home is labelled "Add to my list". It adds to the user's primary list by default.
- When the user can edit more than one list, the add screen has a **List** choice. It shows the lists from the My lists screen, in the same order, and it defaults to the user's primary list. The add button on a list's page defaults to that list. When the user can edit only their primary list, there is no List choice.
- An item's page loads the item, then its list (`GET /lists/{list_id}`), to show the list's name and recipients. The list's name opens the list's page. The recipients follow the rules for a list's page (see [Lists](#lists)).
- An item's page shows what the item shows on its list's page, and adds:
  - all of its images, with the primary image first
  - its notes and its store links
  - "Added by {name}" (see below)
  - for a giver, each of the user's own open gifts on the item (`GET /gifts?item_id={id}&giver_id=me&state=open`), with its quantity and occasion, for example "You claimed 1 for Christmas", and the claim controls
  - the edit controls, when `can_edit` is `true`
- An item's page shows "Added by Mom" from `added_by`, for everyone who can see the item. It doesn't show when `added_by` is the list's only recipient, because that says nothing new, or when `added_by` is `null`. The name opens the user's page only when the user can see that user, as for a recipient. An item's row on a list's page never shows `added_by`.
- Deleting an item opens a confirmation screen whose words never depend on whether the item is claimed, for every user.
- No screen offers reordering an item's images yet.
- The web app never warns about gifts before an item is moved, because that would show a recipient who moves the item whether it is claimed.
- The add screen shows a hint about a duplicate item. The hint is the client's own. The server has no duplicate check (see [Duplicates](#duplicates)).
  - It checks only the list that the add goes to: the list in the List choice, or the primary list when there is no List choice. It loads `GET /lists/{id}/items`, and loads it again when the choice changes. An item on a different list is often wanted there too.
  - An item matches when one of its store links has the URL of the pasted link, as `POST /search` returns it in `store_links`, or when its title is the new title, with case and extra spaces ignored.
  - The hint is one line, for example "Already on this list: Grill", which opens that item. It never blocks the add, and the Add button needs no extra tap.
  - Accepted gap: a parent who is a giver on their child's list doesn't see fully claimed items, so they get no hint for them.

## Search

### Search

- **Search** turns what a user gives into suggested item details. The user checks and changes them before saving. Nothing is saved, so search is separate from adding an item. It reads a link, free text, or a photo, and it doesn't search stores.
- `POST /search` takes a form with exactly one of two fields: `text` (a URL or free text) or `image` (a photo). It always returns `{ results }`. Today a search gives exactly one result. A later store search could give more results without changing this shape.
- What each input gives:
  - **A URL:** the extraction pipeline reads the page in up to three stages:
    1. **Direct fetch.** It follows redirects and short links, and parses JSON-LD `Product`, OpenGraph, and meta tags. If the site blocks the fetch or the page has no useful data, it retries once with a link-preview user agent.
    2. **Bright Data Web Unlocker.** Used only if the direct fetch is blocked or returns no useful data, and only when Bright Data is set up.
    3. **LLM.** Fills in a missing title, price, or image from cleaned page text and candidate image URLs. Used only when the LLM is set up.

    A link never goes to Bright Data or the LLM if its address is on the instance's own network, or if the instance's DNS says its host name doesn't exist. The pipeline caches a result by the normalized URL for 24 hours. The result has the page's title, one store link with the page's price, up to 5 image URLs, and the page's options.
  - **Free text:** the title and the notes are the text as typed. Search calls no LLM for it. A later version could have the LLM turn free text into a title and a price range, as an additive change.
  - **A photo:** the LLM gives a rough title, and a price only when the photo shows one. The server reads the photo and keeps nothing.
- A result uses the field names of the `POST /lists/{id}/items` body, so a client can send it on with the user's changes. The [route table](routes.md#search) lists the fields.
- When no step finds anything, the result has `found: false`. It then has only what the input gave: the URL as a store link, the text as typed, or nothing for a photo. For a photo, this is also the result when the LLM is off or fails.
- A result is only a suggestion. Adding the item cleans and checks every field again, including blocked stores. It does this for details from search, from the device, and from the user's typing.
- **Blocked stores:** a URL from a blocked store, before or after redirects, is a 422 `store_blocked`. The server checks the pasted URL before it fetches anything, so that refusal doesn't count against the hourly limit. A URL that redirects to a blocked store is found only after the fetch, so it counts.
- **Hourly limit:** each user can search at most 30 URLs and photos in any rolling 60 minutes (a sliding window, not fixed resets). Adding a store link and refreshing prices count toward the same limit. Over the limit, search returns 429 `rate_limited`. These don't count:
  - free text
  - a URL read from the 24-hour cache
  - a request that fails its checks before any fetch or LLM call, such as a blocked pasted URL or a file that isn't an image
  - a photo when the LLM is off

### Images from a search

- A result's `image_urls` are on the store's site. To save one with the item, the client sends `{ "url" }` to `POST /images`. The server downloads it, checks it as it checks an upload, and returns an image. The client then adds the item with `image_ids` and `primary_image_id`.
- A download never reaches an address on the instance's own network. Every failure of a download is a 422 `image_fetch_failed`: the URL is on the instance's own network, the download fails, or the file is too large or isn't an image. The client then leaves that image out. A download doesn't count against the hourly limit.
- A photo goes to the server two times: to search for the guess, and to `POST /images` when the user saves. Search stores nothing, so a search that the user leaves makes no image.

### On the device

- A native app can make its own guess from free text or a photo with an on-device model, when the device has one. It fills in the add form itself, and adds the item as usual. This needs no extra route. On other devices, the app uses `POST /search`.
- A URL is always read on the server. An app doesn't fetch or parse a store's page itself.

### Screens

- The paste box and its camera button send to `POST /search`. The add form opens with the result. It is always a pre-filled form that the user confirms: nothing is saved until the user adds the item.
- If every stage fails, the form has the URL and blank fields. While a link is being read, **Fill it in myself** stops the wait and opens the form with the URL and blank fields.
- A photo is taken or chosen from the library. It becomes the item's image. For a photo with no title in the result, the form has the temporary title "Photo from {date}", which the user can change. This includes a photo over the hourly limit.
- Typed words are a free-text item. The paste box has no "Search stores" or "Add as typed", and an item's page has no "Find stores".
- When the user saves, the web app uploads the photo, or each picked image URL, with `POST /images`, then adds the item.
- When the result has images, the form shows them for the user to pick. The first is the primary image.
- If the page lists options such as size or color, the form shows an optional dropdown for each one, preset to the value the link selects, otherwise "Not sure". A chosen value saves into the item's notes, such as "Size: M". If the page names options but lists no values, the notes start with a line for each name, and a line left unchanged is dropped on save. Wishbone doesn't guess an option.
- Over the hourly limit, the form keeps the link and the other fields are blank. A notice says that the link wasn't read.

## Gifts

### Gifts

- A **gift** is one giver's giving of an item: a quantity, for one occasion. A giver starts a gift when they **claim** an item ("I'll get this"). Claim is the action. The gift is the record.
- A gift is one record for its whole life, from when the giver claims the item to the recipients' Received. See [ADR 0011](adr/0011-a-gift-is-one-record-from-claim-to-received.md).

### While a gift is open

- A recipient sees the item as normal, with no sign that it is claimed.
- Other givers don't see a fully claimed item. For a partly claimed quantity, they see how many are still wanted ("3 of 5 still wanted").
- An **unlimited** item never disappears for givers. Each gift on it is tracked separately.
- No one except the giver knows who claimed an item. If someone else claims the item first, the giver sees "Someone else just got this". It never says who.
- The giver picks an occasion (see [Occasions for a gift](#occasions-for-a-gift)). The next one is the default.
- A giver can unclaim a gift before it is given. On screen this is "I won't get this". The quantity goes back to all givers.
- **Giving** is the screen for a giver's open gifts. For each gift it shows:
  - the item, and the reveal date ("Hidden until Dec 26")
  - a **bought** toggle: the giver's private mark that they have acquired the item
  - how many, which the giver can change, up to how many are still wanted (a giver who is a recipient can only lower it, see [Who a gift is for](#who-a-gift-is-for))
  - a private note
  - flags for changes
- Giving groups the gifts by list and occasion. A group with a gift that needs attention goes to the top. A gift needs attention when it is not bought and its reveal is within 14 days, or when it is flagged changed or removed.

### Who a gift is for

- A gift is for the **recipients of its list at the moment of reveal**. The giver doesn't choose recipients. After reveal, the set of recipients doesn't change. If a recipient later leaves the list, the gift stays in their Received. A restore opens the gift again (see [Restore](#restore)). It is then for the recipients at its next reveal.
- A recipient who leaves the list before reveal isn't one of the gift's recipients. A user who joins before reveal is.
- When the list was deleted with the gift kept, the gift is for the list's last recipients, as in [Leaving and deleting](#leaving-and-deleting).
- A gift is never for its giver. When a giver becomes a recipient of a list (see [Invitations](#invitations)), their open gifts on it stay, and are for the other recipients. Example: Mom claims a putter on Dad's list "Golf", and then accepts Dad's invitation to "Golf". At reveal, the putter is given to Dad only.
- When the giver is the only recipient of the list, their open gifts on it are released, as an unclaim does. The quantity goes back to all givers. This happens however the giver gets there: the other recipients leave, or a user who can edit both lists moves the item to a list where the giver is the only recipient.
- While the giver is a recipient of the list, they can lower the gift's quantity, but not raise it. The limit for a raise is how many are still wanted, which is the claim state that recipients don't see. The server refuses every raise with the same 403, whether or not more are still wanted, so the refusal shows nothing. The gift's `can_raise_quantity` field is `false`.

### Occasions for a gift

- A giver picks the gift's occasion from the upcoming occasions of the list's recipients, other than the giver, that the giver can see (see [Who can see a user](#who-can-see-a-user)). The occasions of a recipient are in [Occasions](#occasions).
- Example: Mom and Dad share "Our house". Mom's sister sees the list through Mom, and sees Dad only as a summary. She can pick Christmas or Mom's birthday, but not Dad's birthday. A cousin who sees both can also pick Dad's birthday. Then the gift is given to Mom and Dad together on Dad's birthday.
- Wishbone refuses an occasion that isn't one of these with a 422.
- A giver who can see a list can see at least one of its recipients, and every user has a birthday. So at claim, a giver always has an occasion to pick.
- Later, the giver may see none of the recipients other than themselves. Examples: the giver can no longer see the list, or the list was deleted. When the gift then needs a new occasion (see [Restore](#restore) and [Changes while a gift is open](#changes-while-a-gift-is-open)), the server picks the next upcoming occasion of all the recipients other than the giver. One always exists, because every user has a birthday. The giver can't pick another occasion. They can keep the gift or unclaim it.
- When the gift's occasion isn't an occasion of a recipient that the giver can see, the giver sees only its reveal date ("Hidden until Mar 4"), not its name.
- When a gift moves to an occasion for which its giver already has an open gift on the same item, the gift that was already there takes in the gift that moves:
  - Its ID, bought mark, and from label stay. If it has no from label, it takes the moving gift's from label. The moving gift's quantity is added to it, and the moving gift's flags are added to its flags.
  - If the moving gift has a private note, it is added at the end of the staying gift's note, on a new line.
  - The moving gift is deleted, with any copy from an earlier reveal. Its address is then a 404.
  - So a giver never has two open gifts on one item for one occasion, as [Writes](#writes) requires (`already_claimed`).

### Who sees a gift

- Until it is given, only its giver sees a gift. For everyone else, it is a 404. Other givers see only the item's claimed quantity.
- At reveal, the gift is **given**. These users see it after reveal:
  - its giver
  - its recipients
  - each recipient's parents. Example: Emma is 4, and Mom and Dad are her parents. They see the gifts in Emma's Received.
- A recipient or a parent of a recipient sees what was given, who gave it, the from label, the quantity, and the occasion. They never see the giver's private note or bought mark.
- This is the only way that a parent sees a child's gifts. A parent still can't see the child's Giving or Given (see [Parents and children](#parents-and-children)).

### From label

- A gift has an optional **from label**: a short free text that says who the gift is from, when that isn't only the giver. Example: Mom claims a mug on Dad's list, and the mug is from Emma. Mom sets the label to "Emma". The field is `from_label`, at most 60 characters.
- Only the giver sets and changes the from label. They can change it until the gift is given.
- The from label is a field of the gift, so the same users see it: only the giver before reveal, and the users in [Who sees a gift](#who-sees-a-gift) after reveal.
- The from label doesn't refer to a user. It gives nobody powers over the gift, or a view of it.

### Reveal

- At 00:00 on the day after an occasion, every open gift for that occasion is given. When the recipients have different timezones, the gift is given at the **latest** of their midnights: when the day after the occasion has started for every recipient. Every recipient's timezone counts, including a recipient that the giver can't see. Example: Mom is in New York and Dad is in Los Angeles. The grill is given at 00:00 on Dec 26 in Los Angeles, which is 03:00 in New York.
- A user's timezone comes from the profile. A recipient with no timezone is in the server's `TZ` zone (UTC with no `TZ`): their midnight is the midnight in that zone. If the server is down at the moment of reveal, it gives every gift whose moment has passed when it next starts.
- Wishbone records when the gift is given (`given_at`). Then it copies onto the gift what was given: the item's title, its primary image (as a new image), and its item price. It also copies the occasion's name. Later edits of the item, and a later edit or delete of the occasion, don't change the gift.
- The given quantity leaves the item. Unlimited items and items with quantity left stay on the list.
- **Received** is the view of the gifts given to a recipient, grouped by occasion, with the item, its primary image, and who gave it. Wishbone keeps it forever. **Given** is the giver's view of the gifts they gave, newest first, grouped by occasion, with who each went to. Both are views of the same gifts, not separate records.
- When a giver is deleted, their open gifts are released. Their given gifts stay in the recipients' Received, with the giver's name as it was.

### Restore

- **Restore** ("I didn't get this") says that a given gift wasn't received. It doesn't delete the gift. It reopens it for its giver:
  - The gift is open again. It is no longer in Received. `given_at` is cleared.
  - Its occasion becomes the next upcoming occasion, by the rule in [Occasions for a gift](#occasions-for-a-gift). The giver can pick another occasion, unless they can see none of the recipients other than themselves.
  - The gift is flagged as restored. The flag has the user who restored it, as a user summary, only when the giver can see that user. Otherwise it has no user. The giver sees the flag in Giving.
  - If the item and its list weren't deleted, the gift's quantity goes back on the item, claimed by the giver. Other givers can't claim that quantity. An item that had left its list, because its whole quantity was given, is on the list again.
  - If the item or its list was deleted, the item stays deleted. The gift is also flagged "removed", as a kept gift is (see [Who changes an item](#who-changes-an-item)).
- The giver then keeps the gift, which is given again at the next reveal, or unclaims it ("I won't get this"), which puts the quantity back for all givers.
- The gift keeps the copy from its reveal until the next reveal, so the giver still sees what it was when the item was deleted. When the item still exists, the next reveal copies it again.
- The gift's recipients and their parents can restore it.
- After a restore, the gift is hidden again from everyone except its giver. So restore returns 204 with no body, not the gift. This is an exception to [Writes](#writes).
- When the giver was deleted or is deactivated, restore can't open the gift again. It deletes the gift. If the item and its list weren't deleted, the quantity goes back on the item unclaimed.
- Example: Sue claimed the grill on "Our house". Dad deleted the item before Christmas, and Sue kept the gift, so it was given at reveal. Sue hasn't handed it over yet, so Mom taps "I didn't get this". The grill is back in Sue's Giving, flagged "restored" and "removed", for Mom and Dad's next occasion.

### Add again

- **Add again** makes a new item on a list from a given gift. It copies the gift's title, its image (as a new image), and its price. The gift doesn't change.
- The gift's recipients and their parents can add again. The new item can go on any list that the caller can edit.

### Changes while a gift is open

These events change an open gift:

| Event | Result |
|---|---|
| A user who can edit the list edits a claimed item | The gift is flagged as changed, with a line for each change, and the giver can answer it. Editing the title, notes, quantity, unlimited setting, or item price flags. Changing or clearing the item price flags, and the line shows old → new, or old → none. Setting the item price for the first time, from no price, does not flag. Changing the primary image flags, and the line shows only that the image changed. Changing an image other than the primary image does not flag. |
| A store link is removed, or its price changes | The same flag. The line shows the removed store link, or old → new price. Adding a store link, setting a store link's price for the first time, or a refresh with no price change does not flag. |
| A user who can edit the list deletes a claimed item | The delete is soft: givers stop seeing the item. The gift is flagged "removed". The giver can keep the gift (it is given at reveal as normal) or drop it. See [Who changes an item](#who-changes-an-item). |
| The gift's occasion stops applying | The check is against every recipient, other than the giver, that the giver can see. The gift moves only when its occasion applies to none of them. It moves to the next upcoming occasion of those recipients, and is flagged. The giver can pick another occasion. A reveal that has already happened is final. When the giver can see none of them, the rule in [Occasions for a gift](#occasions-for-a-gift) applies. Example: Sue picked Dad's birthday, and Dad leaves "Our house". The gift moves to Mom's next occasion. |
| The giver can no longer see the list | The gift is flagged "no longer shared with you". The check is against the list: the giver can see none of its recipients. For gifts, a deactivated recipient counts as one that the giver can't see (see [A deactivated recipient](#a-deactivated-recipient)). |
| A recipient joins or leaves the list | Each open gift on the list is flagged with the old and the new recipients ("Now for Mom"), as user summaries. A giver who can see the list already sees its recipients, so the flag shows nothing new. For a giver who can't see the list, the flag has no users. |
| The giver becomes a recipient of the list | The gift stays, for the other recipients, and its quantity can only go down. See [Who a gift is for](#who-a-gift-is-for). |
| The giver is the only recipient of the list | The gift is released. See [Who a gift is for](#who-a-gift-is-for). |
| A recipient is deactivated | The gift stays, and the user stays a recipient of the list. For gifts, no giver can see a deactivated recipient, so the rules above apply: the occasion can move, and when the giver can see none of the list's recipients, with the deactivated recipient not counted, the gift is flagged "no longer shared with you". A giver who is a recipient sees themselves, so their gift isn't flagged. The gift still shows the item and the recipient's name, so the giver knows whose gift it is. See [A deactivated recipient](#a-deactivated-recipient). |
| A recipient is deleted | On a list with other recipients, the recipient leaves it, as above. A gift that only they could receive is deleted. See [Deletion](#deletion). |
| The giver is deactivated or deleted | The gift is released. |

### Screens

- **Claim:** the occasion picker shows the upcoming occasions in [Occasions for a gift](#occasions-for-a-gift). The next one is the default.
- **Giving:** the giver can set and change the from label for each open gift.
- **Received:** a gift with a from label shows "From {label}", and the giver's name beside it, for example "From Emma · claimed by Mom". Without a from label, it shows the giver's name.
- **Add again:** the new item goes on the gift's list by default, if it still exists and the caller can edit it. Otherwise the caller picks a list that they can edit. If the caller is one of the gift's recipients, the default is their own primary list. If not, the default is the primary list of the gift's recipient who is the caller's child. When several are, it is the first of them by name.
- **Giving, restored gift:** the flag says who restored it, when it has a user, for example "Mom says they didn't get this". Without a user, it says "Marked as not received".
- **Giving, recipients changed:** the flag names the new recipients, for example "Now for Mom". Without users, it says "The recipients changed".

## Deactivation and deletion

### Deactivation

- Only the admin deactivates and reactivates a user. Nobody can deactivate the admin (see [The admin](#the-admin)). To deactivate a user who is already deactivated, or to reactivate an active user, changes nothing and returns 200.
- **Deactivation** is a pause. It changes no list, recipient, parent link, group membership, occasion, or invitation. Reactivation brings the user back as they were, with the exceptions in [Reactivation](#reactivation).
- When a user is deactivated:
  - Their sessions are revoked. They can't sign in, and their login links don't work while they are deactivated.
  - Their open gifts are released. Each one is deleted, and its quantity goes back on the item for all givers. The gifts that they already gave stay in their recipients' Received.
- While a user is deactivated, only the admin, their parents, and the user see them (see [Who can see a user](#who-can-see-a-user)). Their children don't see them.

#### A deactivated recipient

- A deactivated user stays a recipient of each of their lists. Their summary stays in each list's `recipients`, because a summary doesn't need visibility.
- A list stays visible to a user who can see one of its other recipients (see [Who can see a list](#who-can-see-a-list)). Their parents still see all of their lists.
- **For gifts, a deactivated recipient counts as a recipient that no giver can see.** This applies to every giver, the user's parents too, and to every gift rule that checks which recipients the giver can see:
  - [Occasions for a gift](#occasions-for-a-gift): no giver can pick the deactivated user's occasions.
  - [Changes while a gift is open](#changes-while-a-gift-is-open): an open gift for one of their occasions moves to the next occasion of another recipient that the giver can see, and is flagged. When the giver can see none of the list's recipients, with the deactivated recipient not counted, the gift is flagged "no longer shared with you". A giver who is a recipient sees themselves, so their gift isn't flagged.
  - A giver can claim on a list only when they can see one of its recipients who is active. So a parent who sees a list only through their deactivated child can't claim on it. The item's `can_claim` is `false` for them.
- Example: Mom and Dad share "Our house", and the admin deactivates Dad. Sue had picked Dad's birthday, so her gift moves to Mom's next occasion. Grandma, who is Dad's parent and shares no group with Mom, still sees "Our house", but she can't claim on it, and her open gifts on it are flagged "no longer shared with you". On "Dad's tools", where Dad is the only recipient, every open gift is flagged "no longer shared with you".
- A gift kept on their list is still for them. Their timezone still counts at reveal, and a gift given at reveal is for them too (see [Reveal](#reveal)). If they are reactivated, it is in their Received.

#### Who acts while a user is deactivated

- A parent of a deactivated user still sees them and all of their lists. They also still see the gifts given to them, and their `timezone` and `deactivated_at`. The parent's powers through that user stop until the user is reactivated. These all stop:
  - login links and sessions, and the profile and occasion edits
  - edits to their lists, and leaving or deleting their lists for them
  - invitations for them: accepting and declining
  - restore and add again on the gifts given to them
- A parent still has the powers that come through another user. For example, a parent of Mom still edits "Our house" while Dad is deactivated.
- The admin can only do these to a deactivated user: reactivate them, change their parent links, or delete them with the CLI. The admin can't add them to a group or remove them from one.
- A deactivated user who is a parent keeps their parent links. They have no powers, because they can't sign in.
- Pending invitations to a deactivated user stay pending. Nobody can accept or decline them until the user is reactivated. A recipient of the list can still withdraw one.
- Nobody can invite a deactivated user to a list.

### Reactivation

- When the admin reactivates a user, everything that deactivation kept is as it was. Login links for them that haven't expired work again.
- These don't come back: the sessions that were revoked, and the open gifts that were released.
- An open gift that moved to another occasion, or was flagged, while the user was deactivated stays as it is.

### Deletion

- Only the CLI deletes a user, with `wishbone delete-user` (see [CLI](#cli)). A delete can't be undone. It refuses to delete the admin.
- **Deletion** is the exit. For the user's lists:
  - They leave each extra list that has another recipient, as in [Leaving and deleting](#leaving-and-deleting). Each open gift on it is flagged with the old and the new recipients (see [Changes while a gift is open](#changes-while-a-gift-is-open)).
  - Each list where they are the only recipient, their primary list too, is deleted as a list delete deletes it: its items go, and nobody sees the list again. Wishbone keeps what the gifts that stay need, as a list delete does. The pending invitations to that list are deleted.
- For gifts:
  - Their open gifts are released, as in deactivation.
  - An open gift that only they could receive is deleted. These are the open gifts on the lists above, and the gifts kept on a list deleted earlier whose last recipient was them. Unlike a list delete by a user, no gift is kept for its giver, because nobody can receive it.
  - A given gift for them and other recipients stays for the other recipients. The user is removed from its recipients. This includes a given gift on a list that the delete removes, when its other recipients left the list after the reveal.
  - A given gift for them alone is deleted. So it also leaves its giver's Given. Its quantity doesn't go back on the item, so a recipient who joins the list later can't tell that it was given.
  - A gift that they gave stays in its recipients' Received, with their name as it was (see [Reveal](#reveal)).
- These also go: their sessions, login links, and passkeys, their parent links in both directions, their group memberships, their avatar, their birthday and custom occasions, and the pending invitations to them.
- An open gift for one of their occasions on a list that stays moves to the next occasion of another recipient, and is flagged (see [Changes while a gift is open](#changes-while-a-gift-is-open)).
- A reference to them becomes `null`, or leaves a list of users:
  - `added_by` on the items that they added becomes `null` (see [Items](#items)), and `invited_by` on the invitations that they sent.
  - A restored flag that names them has no user.
  - A recipients-changed flag drops them from its old and new recipients.
- `delete-user` prints each child who has no parent after the delete, with their ID and name, and says that the admin can link a new parent. It refuses nothing because of them. A child with no parent is a valid state. Example: Dad is the only parent of Emma, who never signs in. After Dad is deleted, nobody edits Emma's lists until the admin links a new parent.

### Screens

- Deactivating a user opens a confirmation screen.
- The web app has no screen that deletes a user.
- Wherever the web app shows a user whose `deactivated_at` the viewer gets (the user's parents and the admin), it shows "Deactivated" beside the user's name. This includes the user's tile in People, in Your children and in each group, their part on the My lists screen, their user page, the Users screen, and a list's recipients.
- A deactivated user stays where they were, in the same order. Nothing hides them from a viewer who sees them.
- For a parent who isn't the admin, a deactivated child's user page also shows one line: "Only the admin can reactivate {name}. Until then, you can't change anything for {name}." For example, "Only the admin can reactivate Emma." The For part doesn't show, because each `can_*` through the child is `false`.

## Home and navigation

### Home

- Home is about giving. From top to bottom, it has:
  1. A header band with a countdown to the next occasion in the next 60 days. The band skips the user's own birthday and custom occasions, which stay in Coming up. Shared occasions count. If two occasions fall on the same day, the band shows both. With no occasion in the next 60 days, it says so. A birthday in the band that has a year also says the age the user is turning.
  2. **My lists** and **Giving**.
  3. The paste box ("Add to my list").
  4. People (see [Users and parents](#users-and-parents)).
  5. Coming up: the occasions in the next 60 days for everyone the user can see. A shared occasion is one row.
  6. Not bought yet: the user's open gifts that need attention (see [While a gift is open](#while-a-gift-is-open)).
- When the next occasion is a shared occasion, the band names it. A shared occasion marked everyone says "Everyone". "See who's on it" opens the shared occasion's page, which lists the users it applies to whom the user can see, each linking to their page. It marks each user "N from you" (the user's own open gifts for that user on the occasion's date) or "Nothing yet".

### Navigation

- Every signed-in screen except the first sign-in steps has a top row: Wishbone on the left, which goes to Home, and Menu on the right. Menu shows the user's avatar and name, links to their other screens, and Log out. It also has a Source code link to the repository, with the version the server reports.
- There are no tab bars and no page-level Back links. People use the browser's Back. In the installed app, which has no browser controls, the top row also has a Back button, except on Home.
- A missing page and a page not shared with the user show the same message.
- Deleting an item, deleting a list, leaving a list, "I won't get this", "I didn't get this", removing a last passkey, deactivating a user, and deleting a group each open a confirmation screen. Its button repeats the exact action. The words for deleting an item or a list never change, so the screen gives no sign that something was claimed.
- Each screen has an empty state and a failure state that say what happened and what to do next.

### Design

- Design for phones first (iPhone Safari), down to a 320px wide window. Every screen also works on a desktop, where the content is centered with a 1200px maximum.
- The app follows the system light or dark mode.
- Simple, lightweight, and fast. No decorative styling. The brief is deliberately not "warm" or "friendly", to avoid the generic look of LLM-generated UIs (see the [rationale](rationale.md#ui-simple-lightweight-and-fast)).
- Grandparent-friendly:
  - large tap targets
  - base text 16px or larger
  - plain wording ("I'll get this")
  - every action has a tap or button, never only a swipe
- The app can be installed as a PWA (manifest, icons, and a service worker that caches the app shell), but every feature works in a normal Safari tab. There is no offline editing and no cached data: data always comes from the API. After a deploy, an installed app reloads on its own, and a browser tab offers a refresh.
- No notifications.

## API principles

Every route follows these rules. The route table applies them to each resource.

### Server and clients

- The server owns every rule that must be true for every client: who sees what, who can do what, what changes state, and which input is valid. It shows these rules through fields, `can_*` fields, flags, and error codes. The Go tests and the route table check them.
- A client owns its defaults, its words, its layout, and which buttons it shows.
- One exception: when the API lets a client leave a field out, the default for that field belongs to the server. Every client then gets the same result. Example: a claim that leaves out the occasion gets the soonest occasion that the giver may pick (see [Occasions for a gift](#occasions-for-a-gift)).
- In each section of this spec, every subsection is a server rule, except **Screens**. A **Screens** subsection says what the web app does. API principles also holds the rules that every client must follow, such as following `next_cursor`. Those rules are not Screens lines. [Operations](#operations) lists what the operator sets and reads, not rules of the API.

### Addresses

- Each resource has **one canonical address**. Every caller uses the same address. The caller's relationship to the resource (for example a recipient of the list, a parent of a recipient, a giver who can see the list, or the admin) decides what they can do and which fields they get.
- A resource that has its own identity gets a top-level address: `/users/{id}`, `/lists/{id}`, `/items/{id}`, `/gifts/{id}`, `/groups/{id}`, `/occasions/{id}`, and `/images/{id}`. A resource has its own identity when other resources refer to it, or when it can move. For example, an item can move to another list. With the address `/items/{id}`, the move changes a field, not the item's address.
- A collection is under its parent, for listing and creating: `GET /lists/{id}/items`, `POST /lists/{id}/items`, `POST /items/{id}/gifts`.
- A part that never leaves its parent, and that nothing else refers to, stays under its parent: `/users/{id}/sessions/{session_id}`, `/users/{id}/login-links`, `/users/{id}/passkeys`.
- `me` is a reserved user ID. It can go anywhere a user ID goes in a path, for example `GET /users/me` or `/users/me/sessions`. The server changes it to the caller's ID before the route runs. `GET /users/me` returns the same response as `GET /users/{the caller's id}`. There is no `/me` tree.
- `current` is a reserved session ID. Signing out is `DELETE /users/me/sessions/current`.
- A response never contains `me` or `current`. The `id` fields, links, and the `Location` header always use the real ID.

### Collections

- A top-level collection returns the resources that the caller can see. For example, `GET /lists` returns every list the caller can see, `GET /users` returns every user the caller can see, and `GET /gifts` returns the gifts the caller can see: the ones they give, the given gifts they received, and the given gifts their children received. Who sees a gift is in [Gifts](#who-sees-a-gift), so there is no `/users/{id}/gifts`.
- Every collection returns this envelope:

  ```json
  { "data": [], "next_cursor": null, "total_count": 0 }
  ```

  - `data` contains full resources (see [Shapes](#shapes)).
  - `next_cursor` is an opaque string, or `null` on the last page. The client sends it back as `?cursor=`. The client can send `?limit=`. The route table gives each route's default and maximum page size.
  - Clients must always follow `next_cursor`. A small collection can return all of its resources on one page. The server can start to page a collection later, and that is not a contract change.
  - `total_count` is the number of resources that the caller can see in the whole collection, not only on this page. The server counts with the same visibility rules that it uses for `data`.
- Filters are query parameters with the name of the field they match: `?list_id=9`. A boolean is `true` or `false`. A repeated parameter means "any of": `?list_id=9&list_id=11`. A filter can match a `can_*` field, for example `?can_edit=true` for the lists that the caller can edit, which includes their children's lists. A filter that is not a field, such as a list's `?recipient_id=`, is defined once in the [route table](routes.md). `?recipient_id=12` returns the lists that have user 12 among their `recipients`. A filter that the route doesn't know is a 422.
- `?sort=<key>` sets the order, and `?sort=-<key>` sets the reverse order. A request has one sort key. The server always adds `id` as the last tiebreaker. The route table gives each route's sort keys and its default. A sort key that the route doesn't allow is a 422. A collection without sort keys has one order, which the route table gives.
- A sort key must be a field that every caller of the route can see. For example, no route sorts by claimed quantity.
- A cursor belongs to its sort. A cursor sent with a different `sort` is a 422 with `code: "cursor_mismatch"`.
- The first sort keys:
  - **Items on a list:** `position` (the default: the order set by the users who can edit the list), `created_at`, and `price`. `price` uses the low end of a price range. Items with no price are last in both directions. Items with different currencies sort by currency code, then by amount.
  - **Users:** `name` (the default) and `created_at`.
- A parent resource has a count of a collection (for example, the number of lists a user has) only where the route table says a screen needs it. That count uses the same visibility rules as the collection.

### Shapes

- Each type of resource has two shapes:
  - **Full**: what the resource's own address returns. Collections contain full resources too. A full resource has every field that the caller can see, and the `can_*` fields.
  - **Summary**: a small reference that goes inside another resource. For example, a user summary is `{ id, name, avatar }`, where `avatar` is an image summary or `null`. A summary has no `can_*` fields. To act on a resource, the client gets its full shape. The route table gives one summary shape for each type.
- A resource contains summaries only for small relationships that have a natural limit: a user's parents and children, and a list's recipients. A resource never contains a collection that grows, such as items, gifts, or sessions. A user doesn't contain their lists: `GET /lists?recipient_id={id}` returns them.
- There are no `?expand=` or `?include=` parameters. Each route returns one shape.
- Field names and query parameters are snake_case: `list_id`, `created_at`, `can_edit`, `is_admin`. A boolean name is a statement: `can_edit`, `is_admin`.
- Path segments are kebab-case, for example `/login-links` and `/refresh-prices`.

### What the caller can do

- Every full resource has a flat `can_<action>` boolean for each action in its section of the route table, for example `can_edit`, `can_delete`, `can_claim`. The value is `true` or `false`, and the field is always there.
- The rules are in the sections of this spec, and each route's rule is in the [route table](routes.md). Each rule is a check on data, for example "edit a list: you are one of its recipients, or you are a parent of one of its recipients." The server applies the same rules to every request. A `can_*` field tells the client what to expect. It doesn't give a permission.
- A `can_*` value must never tell the caller something that a hidden field would. For example, a recipient's view of an item on their list always has `can_claim: false`.

### Values

- A field that the caller can see is always present. When it has no value, it is `null`. A field is absent only when the caller may not see it. A list field with no entries is `[]`, not `null`.
- An ID is a JSON string: `"id": "42"`, `"user_id": "7"`, `"list_id": "9"`. The server keeps integer IDs. The API promises only that an ID is an opaque string that doesn't change while its resource exists, and that it is never used again. Clients must not parse an ID, order by it, or think it has a format. No ID can be `me` or `current`.
- A timestamp is RFC 3339 in UTC with `Z`, for example `"2026-11-05T14:03:00Z"`. Its field name ends in `_at`.
- A date that always has a year is `"YYYY-MM-DD"`, with no time and no time zone.
- A date that can have no year, such as a birthday, is a **partial date** object: `{ "month": 11, "day": 5, "year": null }`. `month` and `day` are always present. `year` is `null` when it isn't known. The server refuses a day that isn't in the month. February 29 is valid with no year, and with a year only in a leap year. The API has only these two date formats.
- Money is an integer amount in minor units and an ISO 4217 `currency`. The amount's field name ends in `_minor`. The API never uses a decimal number for money.
- An enum value is a snake_case string, for example `"open"`.

### Writes

- **Edit fields:** `PATCH` to the resource's address, with JSON Merge Patch (RFC 7396) rules. The server accepts `application/merge-patch+json`, and `application/json` with the same meaning.
  - A field that is present is set.
  - A field that is absent doesn't change.
  - A field sent as `null` is cleared. Only a nullable field can be cleared. Elsewhere, `null` is a 422.
  - An array that is present replaces the whole array.
  - Each resource has its own `PATCH` handler. The server doesn't use huma's `autopatch`. There is no public `PUT` for a resource that has fields, and an image is uploaded with `POST /images`, so the route table has no `PUT` routes.
  - For example, to move an item, a client sends `PATCH /items/{id}` with `{"list_id": "9"}`.
- **Create:** `POST` to a collection. When the result of a change is a new thing, the change is a create. For example, a login link is `POST /users/{id}/login-links`.
- **Other actions:** only when a change isn't a field edit or a create, `POST /{resource}/{id}/{verb}`. The verb is lowercase and describes the change, for example `POST /items/{id}/refresh-prices`. It takes a JSON body if it has inputs. The resource's `can_*` field for the action is `can_` and the verb, with `_` in place of `-`, for example `can_refresh_prices`.
- **Search:** `POST /search` isn't a create or an action. It takes a form (`multipart/form-data`), so that it can take a photo with no upload first. It saves nothing, so it returns 200 with its results (see [Search](#search)).
- **Responses:**
  - A create returns 201, a `Location` header with the new resource's address, and the full resource.
  - A `PATCH` returns 200 and the full resource after the change.
  - An action returns 200 and the full resource that it changed, or 202 if the work continues after the response. A few routes are exceptions. For example, restore returns 204, because the caller can no longer see the gift (see [Restore](#restore)), and a login link's create has no `Location` header, because a login link has no address. The [route table](routes.md#exceptions-to-writes) lists every exception.
  - A `DELETE` returns 204 with no body. A `DELETE` of a resource that is gone, or that the caller can't see, is a 404. A client that sends a `DELETE` again can treat the 404 as done.
  - A write never returns only part of the resource, or a body such as `{"ok": true}`.
- **Concurrent edits:** every full resource response has an `ETag`. The `ETag` is a hash of the response body that the caller gets, so it changes only when something the caller can see changes. Each resource in a collection's `data` has the same value in a `version` field.
  - `PATCH` and `DELETE` accept `If-Match`. When the resource changed since that version, the server returns 412 with `code: "resource_changed"`, and changes nothing.
  - `If-Match` is optional. Without it, the merge-patch rules apply, and the last writer wins for each field.
- **Retries:** the API has no idempotency keys. Where a duplicate would cause a problem, a unique rule refuses it with a 409 and a specific `code`. For example, when a giver claims the same item for the same occasion a second time, the response is `code: "already_claimed"`. A client can treat that as success, and get the gift. Where a duplicate only causes extra work, such as an item that is added two times, the duplicate stays. If retries need more safety later, the fix is an optional `Idempotency-Key` header on `POST`. That is an additive change.

### Errors

- **404**: the resource doesn't exist, or the caller can't see it. The two look the same, so a status never shows that a hidden resource exists.
- **403**: the caller can see the resource, but can't do the action. If a resource has `can_edit: false`, an edit returns 403.
- **401**: the request has no valid session or token.
- A hidden field never causes an error. A recipient who reads an item on their list gets 200, without the hidden fields.
- Other statuses follow huma's rules: 422 for input that isn't valid, 409 for a conflict with the current state, and 429 for a rate limit.
- An error body is RFC 9457 problem+json, with one more field, `code`:

  ```json
  {
    "status": 409,
    "title": "Conflict",
    "code": "item_fully_claimed",
    "detail": "Someone else just claimed the last one.",
    "errors": [{ "location": "body.quantity", "message": "..." }]
  }
  ```

  - `code` is a snake_case string that doesn't change within an API version. Clients use it to decide what to do, and a mobile app uses it to translate the message. Every 4xx and 5xx error has a `code`. The route table gives the codes that each route can return.
  - `detail` is a sentence in plain English. Its words can change.
  - `errors` lists the problems with each field of the input.
  - The API doesn't use `type` URIs to name problems, because a self-hosted instance has no fixed host for them.

### Sign-in

- One middleware finds the caller before a route runs. It takes the session token from `Authorization: Bearer <token>`, or from the `wishbone_session` cookie. If a request has both, the bearer token is used. Routes get the user and the session from the middleware, and never read the cookie.
- A route gives the same response, the same `can_*` values, and the same errors for both. Only cookie renewal (`Set-Cookie`) is for the cookie alone.
- A bearer token is a session. It shows in `/users/{id}/sessions`, and is revoked in the same way as a cookie session.
- The OpenAPI document has two security schemes, `cookieAuth` and `bearerAuth`. They apply to every route except health, setup, and the sign-in routes for a caller who isn't signed in.
- The web app signs in with the cookie. A native app signs in with a bearer token. No native app is part of this repo: a native app is a separate project, and the API supports one. See [ADR 0013](adr/0013-native-apps-redeem-a-login-link-for-a-bearer-session.md).
- A native app gets its token from a login link, by redeeming it with `"session": "bearer"` (see [Sign-in](routes.md#sign-in) in the route table). The app gets the link from its QR code or from a paste. A user who is signed in on the web and has no link can make one for themselves on their sign-in screen.
- The link gives the app its instance: the instance's address is the part of the link's URL before `/login/`. The app calls check, then redeem, on that instance, as the web page does. So one build of an app works with every instance, and needs no domain association.
- A bearer token lasts as a cookie session does: it expires 365 days after its last use, and each request moves that date forward. There is no refresh token, and nothing to renew.
- A native app keeps its token in the platform's secure storage (the Keychain on iOS, Keystore-backed storage on Android), and leaves it out of device backups.
- A native app sends a `User-Agent` that names it, for example `Wishbone iOS/1.0`, because the sessions list shows the user agent.
- Only login link redemption gives a bearer token. Setup and passkey sign-in start cookie sessions only. These can come later, as additive changes:
  - sign-in in a system browser sheet, with a one-time code that the app exchanges for a token with PKCE
  - native passkeys and links that open in the app, which need the instance and the app to name each other (domain association)
  - a `session` field on the passkey sign-in routes, which either of these would need

### Images

- An **image** is a resource at `/images/{id}`.
- An image is different from other resources: its address returns the image's bytes, not JSON.
- `POST /images` takes the bytes of an image file, or `{ "url" }` as JSON, which the server downloads (see [Images from a search](#images-from-a-search)). It returns 201, a `Location` header, and the image as JSON: `{ id, url, width, height, content_type }`. `url` is the address of the bytes, `/api/v2/images/{id}`. An image has no `can_*` fields, because a client can't do anything to an image. The route table gives the size and type limits.
- The client then attaches the image by its ID. A field that holds an image is read as an image summary, and set with its ID in a field that has `_image_id` at the end. For example, a user has `avatar`, and `PATCH /users/{id}` with `{"avatar_image_id": "31"}` sets it. `{"avatar_image_id": null}` removes it. A client can upload an image before the item it goes on exists. An item's images are in [Images on an item](#images-on-an-item).
- An image doesn't change. To replace an image, a client uploads a new one. `GET /images/{id}` returns the bytes with `Cache-Control: private, max-age=31536000, immutable`.
- An image is attached to one resource at most. Its visibility is the visibility of that resource, and the [404 rule](#errors) applies. One exception: a user's avatar is also visible to everyone who gets that user's summary. Before it is attached, only the user who uploaded it can see it. To attach an image that is already attached to a different resource is a 409. When Wishbone copies an image (for example, an item's primary image to a given gift), it copies the bytes into a new image.
- There is no `DELETE /images/{id}`. An image that is no longer attached, or that wasn't attached in 24 hours, is deleted by the server.
- Another resource contains an image as a summary: `{ id, url, width, height }`.
- `GET /images/{id}` uses the same sign-in and visibility as every other route. There are no signed URLs. A store's own image URL (for example, on a store link) is outside Wishbone, and the client loads it directly.

### Versions of the API

- The API is versioned in its path. The current version is at `/api/v2/`. The OpenAPI document at `/api/v2/openapi` is the contract. A test compares the document with a copy in the repo, so a change to the contract shows in the PR diff.
- These changes are allowed within a version:
  - new routes and new actions
  - new response fields, including new `can_*` fields
  - new optional request fields and new filters
  - new enum values and new error codes
  - paging a collection that didn't page before
  - a changed permission rule, when the `can_*` fields show it
- Clients must ignore fields that they don't know. They must accept an enum value that they don't know, for example as an `unknown` case. For a `code` that they don't know, they show `detail`. They always follow `next_cursor`.
- These changes need a new major version (for example `/api/v3/`), which runs at the same time as the current one: removing or renaming a field, changing a field's type or meaning, a new required request field, stricter checks on input that was accepted before, and removing a route.
- The server registers the active API versions from one list (only `2` today). A request to `/api/v{N}/…`, where N is a whole number below the lowest active version, answers 410 Gone with `code: "api_version_removed"`. The rule is for the day a version is retired. A client that was built for that version, such as a web app that a phone cached, would otherwise get an ordinary 404 and show a broken screen. The 410 and its code tell the client that the API has moved on, so it can reload or ask for an update. A version above the highest active one is not registered, and answers as any unknown path does. A malformed version (`vx`, `v01`, `v-1`) is not a version, and answers as an unknown path too.

### Screens

- A client shows its buttons from the `can_*` fields, and doesn't need more requests.
- The web app can show an error's `detail`.
- The web app's paths follow the API's addresses:
  - `/lists` is the My lists screen (see [Lists](#lists)).
  - `/lists/{id}` is a list's page. What it shows depends on who looks, as in the API. For example, a recipient doesn't see the claim state, and a giver does.
  - `/items/{id}` is an item's page. Its path has no list, so a link to it keeps working after the item moves.
  - `/users/{id}` is a user's page.
  - `/users/{id}/profile` and `/users/{id}/sign-in` are a user's profile and sign-in screens, for the user and for anyone who acts for them (see [Users and parents](#users-and-parents)).
  - `/users` is the admin's Users screen (see [Admin and setup](#admin-and-setup)). For anyone else, it shows the missing-page message.
  - `/users/{id}/occasions/new`, `/users/{id}/occasions/{occasion_id}`, and `/users/{id}/occasions/{occasion_id}/delete` add, edit, and delete a custom occasion (see [Occasions](#occasions)).
- There are no redirects. A path that no screen matches opens Home.
- A path that matches a screen, but whose resource is missing or not shared with the user, shows the missing-page message. For example, `/items/{id}` for a deleted item. A missing resource and a hidden one show the same message.
- The web app signs in only with the cookie, and never asks for a bearer token. A script on the page can read a token in browser storage, but it can't read an `HttpOnly` cookie. Safari also deletes storage that scripts write after 7 days of use with no visit to the site, so a token there would sign out a user who visits Wishbone a few times a year.
- The login link page has no "Open in app" button. A custom URL scheme would give the link's token to any app that registers the same scheme.

## Operations

### Configuration

Wishbone reads its settings from environment variables. `serve` also takes each `WISHBONE_` setting as a flag.

| Variable | Purpose |
|---|---|
| `WISHBONE_PUBLIC_URL` | Required. The address people use in their browser, with the scheme `http` or `https`. Login links, QR codes, and the WebAuthn relying party use it. |
| `WISHBONE_PORT` | HTTP port. Default `8080`. |
| `WISHBONE_DATA_DIR` | Location of the SQLite database and images. Default `./data`. The image sets it to `/data`. |
| `WISHBONE_TRUSTED_PROXIES` | CIDRs, separated by commas, whose `X-Forwarded-For` header is trusted. |
| `WISHBONE_RATE_LIMIT_REDEEM` | Login link redemption attempts allowed per minute per client IP. At least `1`. Default `10`. Setup attempts count against it too (see [Setup](#setup)). |
| `WISHBONE_LOG_LEVEL`, `WISHBONE_LOG_FORMAT` | `debug`/`info`/`warn`/`error`, default `info`. `text`/`json`, default `text`. Logs go to stderr. |
| `WISHBONE_LLM_PROVIDER`, `_API_KEY`, `_BASE_URL`, `_MODEL` | The LLM stage of [search](#search) (optional). The provider is `anthropic` or `openai` (an OpenAI-compatible API). With no provider, the LLM is off. `anthropic` needs the API key. The base URL defaults to the provider's public API, and the model to a small, inexpensive model. |
| `WISHBONE_BRIGHTDATA_API_KEY`, `_ZONE` | Bright Data Web Unlocker (optional). Set both or neither. |
| `TZ` | The time zone of the server process. It is the zone of the times in the logs, and the zone of a user who has no timezone: for their reveal moment and the `next_date` of their occasions. It doesn't set any user's timezone. With no `TZ`, the zone is UTC. Stored times stay in UTC. |
| `OTEL_*` | Standard OpenTelemetry SDK settings. Tracing is off unless `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set. `OTEL_TRACES_EXPORTER=none` and `OTEL_SDK_DISABLED=true` turn it off. Only the `http/protobuf` protocol works, and `serve` refuses to start with another. |

- The server refuses to start when a setting is not valid, and says which.
- Cookies are always `Secure`, `HttpOnly`, and `SameSite=Lax`. The reverse proxy terminates TLS.

### Observability

- Structured logs.
- Prometheus `/metrics`, with no sign-in:
  - Go runtime and process metrics
  - extraction success and failure by stage (direct, Bright Data, LLM)
  - LLM token usage, by input and output
  - Bright Data request count, when Bright Data is set up
- Optional OTel tracing on the backend. A span never carries a URL, a query string, or a header, because login links and tokens travel in them.

### Backups

- `wishbone backup <path>` writes a consistent snapshot: SQLite `VACUUM INTO` plus a copy of the images. It is safe to run while the app is live. `<path>` must not exist.
- There are no scheduled backups in the app. Host volume backups and a cron job that runs the backup command cover this.
- Before a migration, `serve` also writes a snapshot of its own. See [Automatic backup](#automatic-backup).

### Deployment

- One distroless, multi-arch image at `ghcr.io/jsclayton/wishbone`, with one volume for `WISHBONE_DATA_DIR`.
- The image runs `wishbone serve`. It has no `HEALTHCHECK` of its own. The `wishbone healthcheck` command is for the container's health check, and the compose example sets it (see `docker-compose.yml`).
- The compose example contains only the wishbone service. A reverse proxy that terminates TLS goes in front of it.

## Migration

Migrations change the database in place, when the server starts. Before it applies any, the server writes a snapshot, and a version guard stops an image from starting on a database that it doesn't support. The trade-off is in [ADR 0012](adr/0012-migrate-in-place-with-an-automatic-backup.md).

### Start order

When `serve` starts, it does these steps in this order:

1. The [version guard](#version-guard).
2. The [automatic backup](#automatic-backup), only when a migration is pending.
3. The migrations.

Only `serve` applies migrations. Every other command that opens the database does step 1. Every command except `backup` also refuses when a migration is pending, and says to start the server first. `backup` only reads the database, and it works on a database that is not yet migrated.

### Automatic backup

- When `serve` starts and goose has migrations to apply, the server first writes a snapshot. A new database gets one too, because all its migrations are pending.
- The snapshot goes to `backups/pre-migrate-<from version>-to-<to version>-<UTC timestamp>/` in the data dir. It has the same contents as a snapshot from `wishbone backup` (`wishbone.db` and `images/`). So the restore steps in [Operations](operations.md#restore) apply to it without change.
- If the snapshot fails, for example because the disk is full or an image is missing, `serve` removes the failed snapshot and exits non-zero. It applies no migration. The server never migrates a database without a snapshot.
- The server keeps the 3 newest automatic snapshots, and deletes older automatic snapshots after a new one is written.
- The server never deletes a snapshot from `wishbone backup`. The operator deletes these by hand. It also never deletes an automatic snapshot that is named for a move from a version below the baseline's (see [The baseline migration](#the-baseline-migration)), because that snapshot can be the only copy of the data it holds. The 3 newest don't count it. This binary writes no such snapshot, because the version guard refuses a database below the baseline.
- When no migration is pending, `serve` writes no snapshot.
- The snapshot reads the database as it is before the migration. For example, it finds the image references with a query for the schema of that database, not of the new binary. Every database version that the binary accepts has such a query.

### Version guard

- Every command that opens the database refuses to start when the database has a goose version that is newer than every migration in the binary. This includes `serve`, `backup`, and the CLI commands. The error says to run a newer image, or to restore a snapshot. Without this guard, an older image would start on a database that a newer image has migrated.
- Every command that opens the database also refuses a database that is older than this release supports: its goose version is above 0 and below `20261110030443`, the version of the baseline migration. The error says that the database is older than this release supports. Wishbone has no upgrade path from such a database. The operator keeps running the release that wrote it, or starts a new instance with an empty data directory.
- In `serve`, the guard runs first, before the automatic backup and before any migration. A refused start writes no snapshot.
- A database that goose never touched, such as an empty one, is not refused: it is a new install. A database at a version from the baseline's up to the newest migration's is not refused.

### The baseline migration

- One baseline migration, `20261110030443_baseline.sql`, creates the whole schema. A new install runs it.
- A schema change is a new migration, made with `just migration <name>`. The baseline is never edited.
- A database whose goose table lists versions that have no migration file, as well as the baseline's, has nothing pending for them: goose ignores those versions.
