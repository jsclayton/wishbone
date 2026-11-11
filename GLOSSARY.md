# Wishbone

The terms of Wishbone. They are the words to use in code, UI copy, and docs. This glossary grows with the [spec](docs/spec.md).

## Users

**User**:
Anyone in the instance with a profile, including the admin. The People section on Home keeps the UI copy "People", but "people" and "person" are not names for this term in code, the API, or docs.
_Avoid_: Member, managed member, person, people, account

**Good to know**:
Markdown text on a user, such as sizes, favorite colors, and allergies. Anyone who can see the user sees it. It describes the user, not one list, so a user with several lists has one good to know.
_Avoid_: Bio, about me, notes, preferences

**Admin**:
The one user who runs the instance: they create users, link parents and children, and manage groups, blocked stores, and shared occasions. The first user, made at setup, is the admin. After that, only the CLI moves the role.
_Avoid_: Organizer, owner, root, superuser

**Setup code**:
A one-time code that the server writes to its log while the instance has no users. It lets the first user create themselves as the admin.
_Avoid_: Bootstrap link, setup token, invite code

**Parent**:
A user that the admin has linked to another user, their **child**. A parent manages the child's sign-in, profile, and lists.
_Avoid_: Sponsor, guardian, manager

**Child**:
A user with one or more parents.
_Avoid_: Kid (in code), managed user, dependent, ward

**Deactivation**:
A pause that the admin puts on a user. The user can't sign in, and only the admin, their parents, and the user see them. Their lists, their place as a recipient, and their parent links stay, and the admin can reactivate them. Only the admin deactivates, and a deactivated user stays a recipient of lists that have other recipients.
_Avoid_: Archive, suspend, disable

**Deletion**:
The permanent removal of a user, done only with the CLI. The user leaves the lists that have other recipients, and the lists where they were the only recipient go. Given gifts shared with other recipients stay for them.
_Avoid_: Remove, purge

**Giver**:
Any user who can see a list and isn't one of its recipients. Givers see claim state; recipients do not. A parent of a recipient who isn't a recipient is a giver too.
_Avoid_: Viewer, shopper

## Access

**Login link**:
A single-use, expiring URL that signs a browser in as a specific user. Shown as a link or a QR code.
_Avoid_: Magic link (in UI copy), invite, token

**Session**:
A signed-in browser for one user. A browser holds at most one session. A native app's bearer token is a session too.
_Avoid_: Device (except in UI copy), login

**Passkey**:
A WebAuthn credential a user adds so they can sign in from a signed-out browser without a login link.
_Avoid_: Password, key

## Groups

**Group**:
A named set of users and other groups. Two users can see each other when some group contains both, directly or through nesting. Only the admin manages groups, and a group has no owners.
_Avoid_: Household, circle, family, team

## Lists

**List**:
A set of items, for one or more recipients. A user can have several lists, and a list can have several recipients.
_Avoid_: Wishlist (in code), registry

**Recipient**:
A user that a list's gifts are for. Which items on the list are claimed is hidden from its recipients. A list's recipients are equal, and none of them owns it.
_Avoid_: Owner, list holder

**Primary list**:
The one list that every user has from the start, with that user as its only recipient. It can't have another recipient, and it can't be deleted. The paste box on Home keeps the UI copy "Add to my list", but "my list" is not a name for this term in code, the API, or docs.
_Avoid_: Main list, default list, my list

**Extra list**:
Any list that isn't a primary list. It can have several recipients, and it can be deleted.
_Avoid_: Secondary list, shared list (a list with one recipient is also an extra list)

**Description**:
Optional markdown text about one list, which anyone who can see the list sees. The users who edit the list change it.
_Avoid_: Notes (an item's notes), list notes, good to know (that is on the user)

**Invitation**:
A recipient's request that another user become a recipient of an extra list. It does nothing until the invited user, or their parent, accepts it.
_Avoid_: Invite (as a noun), share request

## Occasions

**Occasion**:
A dated gift-giving event for a user: their birthday, a shared occasion, or a custom one.
_Avoid_: Event, holiday, date

**Birthday occasion**:
The occasion that every user has for their birthday. It has its own ID, is made with the user, and goes with the user. Nobody edits or deletes it: its date follows the user's birthday, and its name is always "Birthday".

**Custom occasion**:
An occasion that one user, or their parent, defines for that user. The admin's access shows no custom occasion. A parent manages a child's.
_Avoid_: Personal occasion, private occasion

**Shared occasion**:
An occasion that the admin defines once, and that reaches every user in the groups it is attached to, or everyone. Only the admin manages it, and it has no creator.
_Avoid_: Holiday, group occasion

**Everyone**:
A mark on a shared occasion: it applies to every user in the instance.
_Avoid_: All my groups, global, all users

## Items

**Item**:
One thing that a list's recipients want: free text, or a product with store links. It has a quantity, and an optional price, images, and notes. Every user who can edit its list changes it. An item's position on the list is the only way to say "this first".
_Avoid_: Gift, wish, product, secret item

**Store link**:
One place where an item can be bought: a product page at a store, with its own price, currency, and captured date. All store links on an item are interchangeable ways to buy the same thing.
_Avoid_: Offer, link (alone), listing, seller

**Unlimited**:
An item that stays on the list no matter how many times it is claimed. On screen, a giver sees it as "Always wanted". In code and docs the term stays **unlimited**.
_Avoid_: Infinite, recurring

## Adding items

**Search**:
Turning a link, typed words, or a photo into suggested item details, which the user checks and changes before saving. Nothing is saved until the user adds the item. The photo here is the user's input, not an **image**: search keeps no copy of it. Search doesn't look up stores.
_Avoid_: Extract, photo guess, lookup (as names for this action)

**Hourly limit**:
How many links and photos one user can have the server read in any rolling hour.
_Avoid_: Extraction limit, quota

## Gifts

**Gift**:
One giver's giving of an item: a quantity, for one occasion, for the recipients of the item's list. It starts when the giver claims the item, and it is given at reveal. Until then, only the giver sees it. After reveal, its recipients and their parents see it too.
_Avoid_: Claim (as a noun), reservation, pledge, present

**Claim**:
To commit to giving some quantity of an item, which starts a gift. Claim is only a verb. The record is the gift.
_Avoid_: Reserve, pledge

**Bought**:
A giver's private mark that they have acquired a gift. Nobody else sees it.
_Avoid_: Purchased, done

**Reveal**:
The moment, at midnight after an occasion, when the open gifts for that occasion are given and their recipients can see them. With several recipients, it is the latest of their midnights. Reveal changes the gift, and doesn't move it to a separate record.
_Avoid_: Reset, clear, unlock

**Received**:
The view of the gifts given to a recipient. The recipient and their parents see it. It is a view of gifts, not a separate history.
_Avoid_: Archive, gifted, history (alone)

**Restore**:
To say that a given gift wasn't received ("I didn't get this"). The gift opens again for its giver, who keeps it or unclaims it.
_Avoid_: Undo, return, unreceive

**From label**:
Optional free text on a gift that says who the gift is from, when that isn't only the giver, for example "Emma". Recipients and their parents see it after reveal. It doesn't refer to a user.
_Avoid_: Signed by, on behalf of, co-giver

**Giving**:
A giver's view of their gifts that aren't given yet.
_Avoid_: My claims (in UI copy)

**Given**:
A gift's state after reveal. Also the name of a giver's view of the gifts they gave.
_Avoid_: History (in UI copy), gifted

## Images

**Image**:
A visual that a user uploads or that Wishbone saves from a store, such as one of an item's images or a user's avatar. An image never changes, and it belongs to one thing at most.
_Avoid_: Picture, photo, file, attachment, upload

**Primary image**:
The image of an item that is shown first.
_Avoid_: Main picture, cover image

## Stores

**Blocked store**:
A store the admin has blocked for the whole instance. A link from it or its subdomains can't be added as a store link or searched. Existing items keep their links. Only the admin blocks stores.
_Avoid_: Banned store, blacklisted store
