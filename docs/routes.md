# Wishbone Route Table

This table applies the [API principles](spec.md#api-principles) to every resource in the [spec](spec.md). It follows only the server rules, not the **Screens** subsections. Where this table and the spec disagree, the spec is right and this table is wrong.

## Conventions

### Paths

- Every path in this table is under `/api/v2`. For example, `GET /users/me` is `GET /api/v2/users/me`.
- `me` is the caller's user ID, and `current` is the caller's session ID (see [Addresses](spec.md#addresses)).

### Roles

A role is the caller's relationship to the resource on that row. A caller can have several roles. They get every field and action that any one of their roles gives.

| Role | The caller is |
|---|---|
| **public** | anyone, signed in or not |
| **signed in** | any user with a valid session or token |
| **self** | the user at this address |
| **parent** | a parent of that user |
| **admin** | the admin |
| **sees the user** | a user who can see that user (see [Who can see a user](spec.md#who-can-see-a-user)) |
| **recipient** | a recipient of the list |
| **recipient's parent** | a parent of a recipient of the list |
| **giver** | a user who can see the list and isn't one of its recipients. A recipient's parent who isn't a recipient is also a giver. |
| **gift giver** | the user who claimed the gift |
| **gift recipient** | one of a given gift's recipients |
| **gift recipient's parent** | a parent of one of a given gift's recipients |
| **in the group** | a user in the group, directly or through nesting |

For any other caller, a resource is a 404 (see [Errors](spec.md#errors)).

**A deactivated user.** When the user that a parent role comes through is deactivated, the role still gives every read: fields, `GET` routes, and the columns of what each role sees. For example, a parent still sees the gifts given to their deactivated child, and the child's `GET /users/{id}/sessions`, which is empty because deactivation revoked the sessions. It gives no write, and every `can_*` field that it would make `true` is `false` (see [Who acts while a user is deactivated](spec.md#who-acts-while-a-user-is-deactivated)). The **admin** role on a deactivated user gives every read, and of the writes only `reactivate` and `parent_ids`. For gifts, a deactivated recipient counts as one that no giver can see (see [A deactivated recipient](spec.md#a-deactivated-recipient)). A write that is refused only because a user is deactivated is a 403 with `code: "user_deactivated"`. A deactivated user can't sign in, so they have no role of their own.

### Collections

- Every collection has a default page size of 100 and a maximum of 200.
- Each collection row gives its filters and sort keys. The first sort key is the default.
- A filter on a field matches a resource only when the caller sees that field on that resource. So a filter never finds a resource by a value that is hidden from the caller.

### References in a body

- A body field that names another resource by ID (for example `list_id`, `user_id`, `image_id`) must name a resource that the caller can see. Otherwise the request is a 422 with `code: "reference_not_found"`. A missing resource and a hidden one give the same error.
- When the caller can see the resource, but a rule doesn't let them use it there, the request is a 403 with `code: "reference_not_allowed"`, unless the route gives another code (for example, a claim's 422 `occasion_not_allowed`). For example, the gift giver sends their own gift as `from_gift_id`.

### Exceptions to Writes

These routes don't follow the response rules in [Writes](spec.md#writes). Every other route does.

- `POST /users/{id}/login-links` returns 201 with no `Location` header, because a login link has no address.
- `POST /gifts/{id}/restore` returns 204, because the caller can no longer see the gift.
- `PATCH /gifts/{id}` returns the gift that stays when the change merges this gift into another one. See [Gift routes](#gift-routes).
- `POST /invitations/{id}/accept` returns the list, because the invitation no longer exists.
- `POST /search` takes `multipart/form-data`, not JSON, and returns 200, because it saves nothing. See [Search](#search).
- The routes that add to a membership (`POST /groups/{id}/users`, `POST /groups/{id}/groups`, `POST /occasions/{id}/groups`) return 201 with the user or group that was added. Its `Location` header is that resource's own address, because a membership has none.

### Codes on every route

| Status | Code | When |
|---|---|---|
| 401 | `unauthorized` | No valid session or token, on a route that needs one |
| 403 | `forbidden` | The caller can see the resource, but can't do this. A route can give a more specific code. |
| 403 | `field_not_writable` | The body sets a field that the caller can see but can't change, for example `parent_ids` from a parent |
| 404 | `not_found` | The resource doesn't exist, or the caller can't see it |
| 412 | `resource_changed` | `If-Match` doesn't match the current version |
| 413 | `request_too_large` | The body is larger than the route allows. A route can give a more specific code (`POST /images` gives `image_too_large`). |
| 422 | `invalid_input` | The input isn't valid. `errors` says which fields. |
| 422 | `unknown_filter` | The query has a filter that the route doesn't know |
| 422 | `unknown_sort` | The query has a sort key that the route doesn't allow |
| 422 | `cursor_mismatch` | The cursor belongs to a different sort |
| 403 | `user_deactivated` | The caller can see the user, but the user is deactivated, so the caller can't do this to them or through them. See [Roles](#roles). |
| 429 | `rate_limited` | Over a rate limit |
| 500 | `internal_error` | The server failed in a way the caller can't fix. The `detail` says to try again. |

The tables below list only the other codes.

## Summary shapes

| Type | Summary |
|---|---|
| User | `{ id, name, avatar }` |
| List | `{ id, name }` |
| Item | `{ id, title, primary_image }` |
| Occasion | `{ id, kind, name }` |
| Group | `{ id, name }` |
| Image | `{ id, url, width, height }` |

## Health, setup, and the contract

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/health` | public | The container health check (`wishbone healthcheck`) uses it. |
| GET | `/setup` | public | `{ "is_needed": bool }`. See [Setup](spec.md#setup). |
| POST | `/setup` | public | Body: `setup_code`, `name`, `birthday`, `last_name` (optional). Also creates the user's primary list and birthday occasion. 201 with the user. Codes: 422 `setup_code_invalid`, 409 `setup_complete`. |
| GET | `/openapi` | public | The OpenAPI document. The contract. |
| GET | `/docs` | public | The API docs page. |
| GET | `/schemas/{name}` | public | The JSON schemas that the OpenAPI document refers to. |
| any | `/api/v{N}/*` | public | 410 with `api_version_removed`, for N a whole number below the lowest active API version (only `2` is active, so that is any N below 2, such as `/api/v1/*`). The `detail` names the version and the one to use. A version above the highest active one is not registered: it answers as any unknown path (404 `not_found`). A malformed version (`vx`, `v01`, `v-1`) is not a version, and answers the same way. These paths are not under `/api/v2`. See [Versions of the API](spec.md#versions-of-the-api). |

## Sign-in

The sign-in routes check and redeem a login link, and sign in with a passkey (see [Access](spec.md#access)). Redeem has a `session` field, for a native app (see [Sign-in](spec.md#sign-in)).

| Method | Path | Roles | Notes |
|---|---|---|---|
| POST | `/login-links/check` | public | Checks a login link without redeeming it. Code: 401 `login_link_invalid` (the link is unknown, used, or expired). |
| POST | `/login-links/redeem` | public | Body: `token`, `session` (optional: `cookie`, the default, or `bearer`). Redeems a login link and starts a session. Counts against `WISHBONE_RATE_LIMIT_REDEEM`. Code: 401 `login_link_invalid`. See below. |
| POST | `/login/passkey/options` | public | Starts a passkey sign-in. Returns the options for `navigator.credentials.get`, which name no user. Counts against `WISHBONE_RATE_LIMIT_REDEEM`. |
| POST | `/login/passkey` | public | Body: `credential`. Finishes a passkey sign-in and starts a cookie session. 204 with the `wishbone_session` cookie. If the caller's current session is a cookie session, the server revokes it. Counts against `WISHBONE_RATE_LIMIT_REDEEM`, with the options route. Code: 401 `passkey_login_failed`, the same answer for every cause. |
| DELETE | `/users/me/sessions/current` | signed in | Signs out. 204. |

How redeem starts the session depends on `session`:

- **`cookie`** (the default): the response sets the `wishbone_session` cookie, and the body has no token. If the caller's current session is a cookie session, the server revokes it.
- **`bearer`**, for a native app: the body also has `token`, the new session's token, and there is no `Set-Cookie`. If the caller's current session is a bearer token, the server revokes it.

In both, the body has `name` and `initials` (the user that the link signs in), `replaced_session` (`true` when the server revoked the caller's current session), and `switched_user` (`true` when that session was another user's). The token is in this response and nowhere else, because the server keeps only its hash. Setup and passkey sign-in start cookie sessions only.

## Users

### User fields

| Field | Who sees it | Writable by |
|---|---|---|
| `id` | sees the user | — |
| `name` | sees the user | self, parent, admin |
| `last_name` | sees the user | self, parent, admin |
| `avatar` (image summary or `null`) | sees the user | self, parent, admin, with `avatar_image_id` |
| `birthday` (partial date) | sees the user | self, parent, admin. Never `null`. |
| `good_to_know` | sees the user | self, parent, admin |
| `is_admin` | sees the user | nobody. The CLI sets it. |
| `parents` (user summaries) | sees the user | admin, with `parent_ids` |
| `children` (user summaries) | sees the user | nobody. A child's `parent_ids` sets it. |
| `primary_list` (list summary) | sees the user, not counting the admin's access | — |
| `timezone` | self, parent, admin | self, parent, admin |
| `profile_confirmed_at` | self, parent, admin | — (see `confirm-profile`) |
| `deactivated_at` | self, parent, admin | — (see `deactivate`) |
| `created_at` | sees the user | — |

`can_*` fields on a user:

| Field | `true` when the caller is |
|---|---|
| `can_edit` | self, parent, or admin, and the user is active |
| `can_edit_parents` | admin, and the user isn't the admin |
| `can_make_login_link` | self, parent, or admin, and the user is active |
| `can_manage_sessions` | self, parent, or admin, and the user is active |
| `can_deactivate` | admin, and the user isn't the admin and is active |
| `can_reactivate` | admin, and the user is deactivated |
| `can_confirm_profile` | self, and `profile_confirmed_at` is `null` |
| `can_create_list` | self or parent, and the user is active |
| `can_add_occasion` | self or parent, and the user is active |

### User routes

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/users` | signed in | The users the caller can see. Filters: `?group_id=` (the users who are in that group, directly or through nesting, and whom the caller can see; not a field; a group the caller can't see, or that doesn't exist, gives an empty result, so the filter doesn't show whether it exists; Home's People needs it, because `GET /groups/{id}/users` lists only the users directly in a group), `?occasion_id=` (users that the occasion applies to, not a field), `?invitable_to=` (a list ID: the users the caller can invite to that list, not a field; see below). Sort: `name`, `created_at`. |
| POST | `/users` | admin | Body: `name`, `birthday`, `last_name`, `parent_ids`, `timezone` (all but `name` and `birthday` optional). Also creates the user's primary list and birthday occasion. 201. |
| GET | `/users/{id}` | sees the user | 404 for a user whom the caller sees only as a summary. |
| PATCH | `/users/{id}` | self, parent, admin | Fields as in the table above. Codes: 422 `birthday_required` (`birthday: null`), 422 `parent_loop`, 422 `admin_has_no_parents`. |
| POST | `/users/{id}/confirm-profile` | self | Sets `profile_confirmed_at`. 200 with the user. |
| POST | `/users/{id}/deactivate` | admin | Revokes the user's sessions and releases their open gifts. 200 with the user. A user who is already deactivated doesn't change. Code: 403 `admin_cannot_be_deactivated`. |
| POST | `/users/{id}/reactivate` | admin | 200 with the user. An active user doesn't change. |

`?invitable_to={list_id}` returns the users that `POST /lists/{id}/invitations` would accept from the caller: users the caller can see, with the admin's access not counted, who are active, aren't recipients of the list, and have no pending invitation to it. It is empty when the caller can't invite to the list: the caller isn't a recipient, the list is a primary list, or the caller can't see the list. The Invite button's list of users needs it, because the admin's `GET /users` includes users that the admin can't invite.

Only the admin, the user's parents, and the user see a deactivated user. A deactivated user stays a recipient of their lists, and the `can_*` fields above that need an active user are `false` for everyone. There is no route that deletes a user: only the CLI does. See [Deactivation and deletion](spec.md#deactivation-and-deletion).

### Login links

| Method | Path | Roles | Notes |
|---|---|---|---|
| POST | `/users/{id}/login-links` | self, parent, admin | 201 with `{ url, expires_at }`, and no `Location` header. The link expires after 7 days. |

A login link has no address. The server keeps only a hash of its token, so the URL is in the create response and nowhere else. There is no route to list or revoke login links. This is one of the [exceptions to Writes](#exceptions-to-writes). A route to revoke them can come later, as an additive change.

### Sessions

Session fields: `id`, `user_agent`, `last_used_at`, `created_at`, `is_current`. Who sees them: self, parent, admin. `can_*` fields: `can_delete` (`true` for each of them).

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/users/{id}/sessions` | self, parent, admin | Sort: `last_used_at` (newest first). |
| DELETE | `/users/{id}/sessions/{session_id}` | self, parent, admin | Revokes the session. 204. |

### Passkeys

Passkey fields: `id`, `name`, `created_at`, `last_used_at`. Who sees them: self, parent, admin. `can_*` fields: `can_edit` (`true` for self), `can_delete` (`true` for self, parent, and admin). Only the user adds and renames a passkey.

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/users/{id}/passkeys` | self, parent, admin | Sort: `created_at` (the default, oldest first). |
| POST | `/users/{id}/passkeys/registration` | self | Starts a registration. Returns the options for `navigator.credentials.create`, which the user has 5 minutes to answer. |
| POST | `/users/{id}/passkeys` | self | Body: `credential`, `name` (optional, 1 to 100 characters, default "Passkey"). Finishes the registration. 201. Codes: 400 `passkey_registration_expired` (no registration is under way, or it ran out), 400 `passkey_not_verified`, 409 `passkey_already_added`. |
| PATCH | `/users/{id}/passkeys/{passkey_id}` | self | Body: `name`. |
| DELETE | `/users/{id}/passkeys/{passkey_id}` | self, parent, admin | 204. |

A passkey has no address of its own to `GET`: the list holds each passkey's `version`, and the 201 and the `PATCH` return the passkey with its `ETag`. A deactivated user can't sign in with a passkey either (see [Deactivation](spec.md#deactivation)).

## Groups

Group fields: `id`, `name`, `created_at`, `through`. `through` is a group summary, or `null`: the group that the caller is directly in, and that this group contains by nesting. For example, a user directly in My family gets In-laws with `through` set to My family. It is `null` when the caller is directly in the group, or isn't in it. When several groups put the caller in this one, it is the first of them by name. Home's People needs it to show "through {group}". `can_*` fields: `can_edit`, `can_delete`, `can_add_user`, `can_add_group`. Each is `true` only for the admin.

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/groups` | signed in | The groups the caller is in, directly or through nesting. The admin gets every group. Filter: `?includes_me=true` (only the groups the caller is in, directly or through nesting, not a field). `false`, or leaving it out, gives the default result. It changes the result only for the admin, and Home's People needs it. Sort: `name`. |
| POST | `/groups` | admin | Body: `name`. 201. |
| GET | `/groups/{id}` | in the group, admin | |
| PATCH | `/groups/{id}` | admin | Body: `name`. |
| DELETE | `/groups/{id}` | admin | 204. |
| GET | `/groups/{id}/users` | in the group, admin | The users directly in the group, as full users. The caller shares the group with each of them, so they can see them. A deactivated user is left out, except for the admin, their parents, and the user, as in [User routes](#user-routes). Sort: `name`. |
| POST | `/groups/{id}/users` | admin | Body: `user_id`. 201 with the user. Codes: 403 `user_deactivated`, 409 `already_in_group`. |
| DELETE | `/groups/{id}/users/{user_id}` | admin | 204. Code: 403 `user_deactivated`. |
| GET | `/groups/{id}/groups` | admin | The groups directly in the group. Sort: `name`. Only the admin, because a user in the outer group isn't in the groups inside it (see [Groups](spec.md#groups)). |
| POST | `/groups/{id}/groups` | admin | Body: `group_id`. 201 with the group. Codes: 409 `already_in_group`, 422 `group_loop`. |
| DELETE | `/groups/{id}/groups/{group_id}` | admin | 204. |

## Lists

### List fields

| Field | Who sees it | Writable by |
|---|---|---|
| `id` | anyone who can see the list | — |
| `name` | anyone who can see the list | recipient, recipient's parent |
| `description` (markdown or `null`) | anyone who can see the list | recipient, recipient's parent |
| `is_primary` | anyone who can see the list | — |
| `recipients` (user summaries) | anyone who can see the list | — (invitations and leaving) |
| `item_count` | anyone who can see the list | — |
| `created_at` | anyone who can see the list | — |

`item_count` is the number of items on the list that the caller can see. A giver doesn't see a fully claimed item, so a giver's count can be lower than a recipient's. The My lists screen and a user's page need it, to show how many items each list has.

`can_*` fields on a list:

| Field | `true` when |
|---|---|
| `can_edit` | the caller is a recipient, or the parent of a recipient who is active |
| `can_delete` | the list is an extra list with one recipient, and the caller can edit it |
| `can_invite` | the list is an extra list, and the caller is a recipient |
| `can_leave` | the list has more than one recipient, and the caller is a recipient. A parent who makes their child leave uses the child's row in `GET /lists/{id}/recipients`. |

### List routes

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/lists` | signed in | The lists the caller can see, with the admin's access not counted. Filters: `?recipient_id=` (lists that have this user among `recipients`, not a field), `?can_edit=`, `?is_primary=`. Sort: `name`, `created_at`. |
| POST | `/lists` | signed in | Creates an extra list. Body: `name`, `description` (optional), `recipient_id` (optional, the caller by default, or one of the caller's children). The caller must be that user or their parent. 201. |
| GET | `/lists/{id}` | anyone who can see the list | |
| PATCH | `/lists/{id}` | recipient, recipient's parent | Body: `name`, `description`. Code: 403 `user_deactivated` (the caller can edit the list only as the parent of a deactivated recipient). |
| DELETE | `/lists/{id}` | see `can_delete` | Deletes the list and its items, as in [Leaving and deleting](spec.md#leaving-and-deleting). 204. Codes: 403 `primary_list`, 403 `list_has_other_recipients`, 403 `user_deactivated` (as for `PATCH`). |
| GET | `/lists/{id}/recipients` | anyone who can see the list | The list's recipients, as recipient resources (below). Sort: `created_at` (when they became a recipient). |
| DELETE | `/lists/{id}/recipients/{user_id}` | that user, or their parent | The user leaves the list. 204. Codes: 403 `primary_list`, 403 `last_recipient`, 403 `user_deactivated` (the user is deactivated, and the caller is their parent). |

A **recipient resource** is `{ user, created_at, sees_list_after_leaving, can_delete }`, where `user` is a user summary. Its address is `/lists/{id}/recipients/{user_id}`. `can_delete` is `true` when the list is an extra list with another recipient, and the caller is that user or the parent of that user while they are active. A list's `recipients` field stays a list of user summaries.

`sees_list_after_leaving` is `true` when that user could still see the list after they leave it: they can see another of its recipients, with the admin's access not counted (see [Who can see a list](spec.md#who-can-see-a-list)). Only that user and their parents get it. For everyone else it is left out. The leave confirmation needs it to show its warning (see the Screens in [Lists](spec.md#lists)).

### Invitations

Only pending invitations exist. Accepting or declining one removes it.

Invitation fields: `id`, `list` (list summary), `user` (the invited user, a summary), `invited_by` (user summary, or `null` if that user was deleted), `created_at`. `can_*` fields: `can_accept` (the invited user or their parent, while the invited user is active), `can_delete` (a recipient of the list, or the invited user or their parent while the invited user is active).

Who sees an invitation: the invited user, their parents, and the list's recipients. For everyone else it is a 404.

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/invitations` | signed in | The invitations the caller can see. Filters: `?list_id=`, `?user_id=` (the invited user, from the `user` field). Sort: `created_at`. |
| POST | `/lists/{id}/invitations` | recipient | Body: `user_id`, a user the caller can see, with the admin's access not counted. A user whom the caller sees only through the admin's access is a 403 `reference_not_allowed`. 201. Codes: 403 `primary_list`, 403 `user_deactivated` (the invited user is deactivated), 409 `already_invited`, 409 `already_recipient`. |
| GET | `/invitations/{id}` | see above | |
| POST | `/invitations/{id}/accept` | invited user, their parent | The user becomes a recipient. 200 with the **list**, because the invitation no longer exists. |
| DELETE | `/invitations/{id}` | invited user, their parent, recipient | Declines or withdraws. 204. |

## Items

### Item fields

| Field | Who sees it | Writable by |
|---|---|---|
| `id`, `created_at` | anyone who can see the list | — |
| `list_id` | anyone who can see the list | a user who can edit both lists (a move) |
| `title` | anyone who can see the list | recipient, recipient's parent |
| `notes` | anyone who can see the list | recipient, recipient's parent |
| `price_minor`, `top_price_minor`, `currency` | anyone who can see the list | recipient, recipient's parent |
| `quantity`, `is_unlimited` | anyone who can see the list | recipient, recipient's parent |
| `position` | anyone who can see the list | recipient, recipient's parent. A 0-based index on the list. The other items shift. |
| `store_links` | anyone who can see the list | through the store link routes |
| `images`, `primary_image` | anyone who can see the list | recipient, recipient's parent, with `image_ids` and `primary_image_id` |
| `added_by` (user summary or `null`) | anyone who can see the list | — |
| `still_wanted` | giver | — |
| `my_claimed_quantity` | giver | — |

- `still_wanted` is how many other givers can still claim. It is `null` for an unlimited item.
- `my_claimed_quantity` is the sum of the caller's own open gifts on the item. The gifts themselves are in `GET /gifts?item_id=`.
- A giver doesn't see a fully claimed item. For them, it is a 404 and it isn't in the collection, unless they have an open gift on it.

`can_*` fields on an item:

| Field | `true` when |
|---|---|
| `can_edit` | the caller can edit the item's list (see `can_edit` on a list) |
| `can_delete` | same as `can_edit` |
| `can_refresh_prices` | same as `can_edit`, and the item has a store link |
| `can_claim` | the caller is a giver who can see a recipient of the list who is active, and `still_wanted` is above 0 or the item is unlimited. Always `false` for a recipient. |

A client that has no list to name, such as a share sheet, adds to the caller's primary list: it reads `primary_list` from `GET /users/me`, and posts to `/lists/{that id}/items`. A client can keep that ID, because a primary list is never deleted.

There is no `can_move`. A move needs edit rights on the other list too, and `GET /lists?can_edit=true` gives those lists.

### Item routes

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/lists/{id}/items` | anyone who can see the list | Sort: `position`, `created_at`, `price` (see [Collections](spec.md#collections)). |
| POST | `/lists/{id}/items` | recipient, recipient's parent | Body: `title` (optional with `from_gift_id`), and optionally `notes`, the price fields, `quantity` (1 by default), `is_unlimited`, `store_links` (each with `url`, and optionally `price_minor`, `currency`, `image_url`, at most 10), `image_ids`, `primary_image_id`, `from_gift_id`. 201. Codes: 422 `too_many_store_links`, 422 `too_many_images`, 422 `store_blocked`, 409 `image_attached`, 403 `user_deactivated` (the caller can edit the list only as the parent of a deactivated recipient, also for `from_gift_id` when the caller receives the gift only through a deactivated child). There is no duplicate check (see [Duplicates](spec.md#duplicates)). |
| GET | `/items/{id}` | anyone who can see the list | |
| PATCH | `/items/{id}` | recipient, recipient's parent | Fields as above. A new `list_id` moves the item. The caller must be able to edit both lists, or it is a 403 `cannot_edit_target_list`. `image_ids` replaces the whole array of images, in the order given, and the server deletes any image left out. `primary_image_id` picks the primary image: it must be one of the images after the change, and `null` picks the first. `image_ids` can't be `null` (send `[]`). `store_links` and `images` are read-only here: a store link changes through its own routes, and the images through `image_ids`. Codes: as for create, and 422 `reference_not_found` for an image the caller can't see (or one that was removed from an item: upload it again). The same 403 `user_deactivated` as for create, also when the other list of a move is the one that the caller can edit only through a deactivated child. |
| DELETE | `/items/{id}` | recipient, recipient's parent | Soft when it has open gifts, as in [Who changes an item](spec.md#who-changes-an-item). 204. Code: 403 `user_deactivated`. |
| POST | `/items/{id}/refresh-prices` | recipient, recipient's parent | Reads each store link's page again, skipping the 24-hour cache, and saves the new price with the time it was captured. It changes only the store links' prices, never the item price. Each read counts against the hourly limit. A store link that can't be read, has no readable price, or is in a store blocked since it was added keeps its price and date. The answer does not say which links were read, and `captured_at` has whole-second precision, so a client can see which prices changed (from the item), but cannot tell a read that found the same price from a read that failed. 200 with the item. When the limit stops the refresh, the prices read before it are saved and the answer is the item. When it stops it before the first page is read, the answer is a 429 `rate_limited` and nothing changes. A read that fails or finds no price counts as a read, because the fetch happened. Code: 403 `user_deactivated` (as for create). |

### The item price

The item price is the price givers see. A store link has a price of its own, which a refresh and a hand edit change without changing the item price (ADR 0005, with store link in place of offer).

- **It starts from a store link.** An item with no price takes the price (and currency) of a store link when the link gets one, once: when the item is created with store links, when a store link is added, and when a user sets a store link's price by hand. It takes the first price any link gets. A range (`top_price_minor`) is never taken. A refresh never sets the item price.
- **A price the user clears stays cleared.** A `PATCH /items/{id}` that sets `price_minor` to `null` on an item that has a price marks the item as cleared. A cleared item never takes a price from a store link again. The user can set a price of their own at any time. Clearing an item that has no price marks nothing. The mark is `items.price_cleared`. It isn't a field of the API.
- **On create, `price_minor: null` and a missing `price_minor` mean the same:** no price chosen yet, so the item takes the price of its first priced store link. A client can send a search result on unchanged (a result has `price_minor: null` for a link, because the page's price is on its store link) and the item gets the store link's price. An item can't be created with a store link that has a price and no price of its own: the user clears the price after the add, and it then stays cleared.

**Add again** is `POST /lists/{id}/items` with `from_gift_id`. The new item copies the gift's title, image (as a new image), and price. Fields in the body win over the copy: a `title`, `image_ids` (an empty array means no image), and the price fields as a group (`price_minor`, `top_price_minor` and `currency` come from the body if it has any of them, and from the gift otherwise). The caller must be a gift recipient or a gift recipient's parent. Otherwise it is a 403 `reference_not_allowed` for the gift giver, and a 422 `reference_not_found` for everyone else. A parent whose child receives the gift while deactivated gets a 403 `user_deactivated`. The gift's `can_add_again` tells a client to show the button.

### Store links

Store link fields: `id`, `url`, `store`, `price_minor`, `currency`, `captured_at`, `image_url`, `created_at`. Who sees them: anyone who can see the list. A caller who can edit the list gets them in the order added. Every other caller gets them sorted by price (see [Store links](spec.md#store-links)). `can_*` fields: `can_edit`, `can_delete` (the users who can edit the list).

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/items/{id}/store-links` | anyone who can see the list | One page, in the order above (an item has at most 10). No sort keys, no filters. |
| GET | `/items/{id}/store-links/{store_link_id}` | anyone who can see the list | |
| POST | `/items/{id}/store-links` | recipient, recipient's parent | Body: `url`. The server reads the page for its price (and the store's picture), and the read counts against the hourly limit unless it comes from the cache. A page that can't be read gives a store link with no price. An item with no price takes the price, as [The item price](#the-item-price) says. 201 with the store link. Codes: 403 `user_deactivated` (as for create), 422 `too_many_store_links`, 422 `store_blocked` (before the read, and for a link that redirects to a blocked store after it, which counts), 429 `rate_limited` (nothing is added). |
| PATCH | `/items/{id}/store-links/{store_link_id}` | recipient, recipient's parent | Body: `price_minor`, `currency`. `null` clears the price and its currency. The captured date becomes now when the change sets or clears a price. A price with no currency keeps the link's currency, or is in USD. Codes: 422 `invalid_input`, 403 `user_deactivated` (as for create). |
| DELETE | `/items/{id}/store-links/{store_link_id}` | recipient, recipient's parent | 204. The item stays, with the price it has. Code: 403 `user_deactivated` (as for create). |

### Search

The rules are in [Search](spec.md#search). Nothing here is saved.

| Method | Path | Roles | Notes |
|---|---|---|---|
| POST | `/search` | signed in | Body: `multipart/form-data` with exactly one of `text` (a URL or free text) or `image` (a file, with the limits of `POST /images`). 200 with `{ results }`. A URL or a photo counts against the caller's hourly limit, as [Search](spec.md#search) says. Codes: 422 `store_blocked`, 422 `image_invalid`, 413 `image_too_large`. |

A result has these fields, with the names of the `POST /lists/{id}/items` body:

| Field | Value |
|---|---|
| `title` | The title, or `null`. For free text, the text as typed, cut to the longest title. |
| `notes` | For free text, the text as typed, up to the longest notes. Otherwise `null`. |
| `notes_cut` | `true` when the free text was longer than the longest notes. |
| `price_minor`, `top_price_minor`, `currency` | The item price, or `null`. A photo gives one only when the photo shows a price. For a URL, the page's price is on the store link, and these are `null`. `top_price_minor` is always `null` today. |
| `store_links` | For a URL, one store link: `{ url, store, price_minor, currency, image_url }`. `url` is the normalized URL, after redirects. Otherwise empty. |
| `image_urls` | Up to 5 of the page's images, best first, with no repeats. A client saves a picked one with `POST /images` and `{ "url" }`. Otherwise empty. |
| `options` | The page's options, at most 4: `{ name, values, selected }`. Otherwise empty. |
| `found` | `false` when nothing was read from the input: always for free text, and for a URL or a photo when no step found anything. The result then has only what the input gave. |

## Images

| Method | Path | Roles | Notes |
|---|---|---|---|
| POST | `/images` | signed in | Body: the image bytes, or `application/json` with `{ "url" }`, which the server downloads. At most 10 MiB. JPEG, PNG, GIF, or WebP. The server stores a JPEG at most 1200 px on its longest side. 201 with `{ id, url, width, height, content_type }`. A download doesn't count against the hourly limit. Codes: 422 `image_invalid`, 413 `image_too_large` (for bytes), 422 `image_fetch_failed` (for a URL: it is on the instance's own network, or the download fails, is too large, or isn't an image). |
| GET | `/images/{id}` | who can see the resource it is attached to; for an avatar, also anyone who gets that user's summary; before it is attached, the user who uploaded it | The bytes, with `Cache-Control: private, max-age=31536000, immutable`. |

## Occasions

### Occasion fields

| Field | Who sees it | Writable by |
|---|---|---|
| `id` | see below | — |
| `kind` | see below | — (`birthday`, `shared`, or `custom`) |
| `name` | see below | custom: the user and their parent. Shared: admin. Birthday: nobody. |
| `rule` | see below | as `name`. A birthday's rule comes from the user's `birthday`. |
| `user` (user summary, or `null` for a shared occasion) | see below | — |
| `is_everyone` (`null` unless shared) | see below | admin |
| `next_date` (`"YYYY-MM-DD"`, or `null` when none is upcoming) | see below | — |

**Whose "today" `next_date` uses.** A birthday and a custom occasion use the timezone of their user (the server's `TZ` zone when the user has none, UTC with no `TZ`), so they don't reveal it. A shared occasion has no user, so it uses the **caller's** timezone (the server's `TZ` zone when the caller has none). Wherever the caller reads the same shared occasion, in `GET /occasions`, in `GET /occasions?user_id=` for any user, or at its own address, its `next_date` is the same, and nobody learns another user's timezone from it. The date rule is the same for everyone, so callers in different zones differ by at most a day, around the occasion's own date. `?next_date_before=` and the `next_date` sort use the same dates.

`rule` is `{ kind, month, day, year, weekday, n }`, with `kind` one of `annual`, `once`, `nth_weekday`, `last_weekday`.

Who sees an occasion: a birthday is seen by every user who sees its user. A custom occasion is seen by the same users, but the admin's access doesn't count, because it gives no access to custom occasions (see [The admin](spec.md#the-admin)). A shared occasion is seen by every user it applies to, by every user who sees a user it applies to, and by the admin, who manages it. A shared occasion applies to the users in the groups it is attached to, directly or through nesting, or to every user when it is marked everyone. Every user sees themselves, so a shared occasion marked everyone is seen by every user. Everyone sees `is_everyone`; only the admin changes it.

**Every birthday is an occasion with its own ID.** Wishbone creates it with the user. Nobody can edit or delete it, and its date follows the user's `birthday`. So a gift names its occasion by ID, whatever the kind. A birthday's `name` is always "Birthday", so the copy at reveal has a name. A client can word it from `kind` and `user` instead, for example "Emma's birthday". It goes only when its user is deleted.

`can_*` fields: `can_edit`, `can_delete` (custom: the user or their parent; shared: admin; birthday: nobody), `can_attach_group` (shared, not marked everyone: admin). For a user who isn't allowed, each is `false`. `can_attach_group` is `false` for a birthday and a custom occasion.

### Occasion routes

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/occasions` | signed in | The occasions the caller can see. Filters: `?user_id=` (occasions that apply to this user: the user's birthday and custom occasions, and the shared occasions that apply to them, the last only when the caller sees the user; not a field), `?list_id=` (the occasions a giver can pick for a gift on this list, see [Occasions for a gift](spec.md#occasions-for-a-gift), limited to the occasions that the caller can see; not a field), `?kind=`, `?next_date_before=` (a date, `YYYY-MM-DD`; keeps the occasions whose `next_date` is on or before it, so it includes the date it names, and never one with a `null` `next_date`; not a field). Sort: `next_date` (soonest first, `null` last in both directions), `name`. |
| POST | `/occasions` | see notes | Body: `kind` (`custom` or `shared`), `name`, `rule`. For `custom`: `user_id` (the caller by default), and the caller must be that user or their parent. For `shared`: admin only (403 `forbidden` for anyone else), no `user_id`, and optionally `is_everyone` (a shared occasion is attached to no groups at first). A 422 `invalid_input` for `is_everyone` on a custom occasion and for `user_id` on a shared one. 201. |
| GET | `/occasions/{id}` | see above | |
| PATCH | `/occasions/{id}` | see `can_edit` | Body: `name`, `rule`, and `is_everyone` for a shared occasion. Marking it everyone detaches its groups, and unmarking it leaves it attached to none. `is_everyone` is a 403 `field_not_writable` on a custom occasion. A shared occasion is a 403 `forbidden` for any caller but the admin. |
| DELETE | `/occasions/{id}` | see `can_delete` | 204. Code: 403 `birthday`. A shared occasion's attachments go with it. |
| GET | `/occasions/{id}/groups` | admin | The groups a shared occasion is attached to directly. Sort: `name`. A 404 for an occasion the caller can't see and for one that isn't shared, and a 403 for a caller who sees it and isn't the admin. The same holds for `POST` and `DELETE` below. |
| POST | `/occasions/{id}/groups` | admin | Body: `group_id`. 201 with the group. Codes: 409 `occasion_is_everyone`, 409 `already_attached`. |
| DELETE | `/occasions/{id}/groups/{group_id}` | admin | 204. A group that isn't attached is a 404. The group stays. |

`GET /occasions?next_date_before=<today + 60 days>` gives the occasions in the next 60 days. `GET /users?occasion_id=` gives the users that an occasion applies to, limited to the users the caller can see. For an occasion the caller can't see, or that doesn't exist, it is an empty page, like `?group_id=`.

## Gifts

### Gift fields

| Field | Gift giver, open | Gift giver, given | Gift recipient or their parent (given only) |
|---|---|---|---|
| `id`, `state` (`open` or `given`), `quantity` | yes | yes | yes |
| `item` (item summary) | yes | yes | yes |
| `list` (list summary) | yes | yes | yes |
| `title`, `image`, `price_minor`, `top_price_minor`, `currency` | yes: the item's now. A deleted item keeps its fields, so they show what it was when it was deleted. After a restore, the copy from the last reveal, until the next one (see [Restore](spec.md#restore)). | yes: the copy from reveal | yes: the copy from reveal |
| `recipients` (user summaries) | yes: the list's recipients now, without the giver (a gift is never for its giver), or absent when the giver can't see the list | yes: the recipients at reveal, or absent when the giver couldn't see the list at reveal | yes: the recipients at reveal |
| `giver` (user summary or `null`), `giver_name` | yes | yes | yes |
| `from_label` | yes | yes | yes |
| `occasion` (occasion summary or `null`), `occasion_name` | yes: the occasion now. Both are `null` when it isn't an occasion of a recipient the giver can see, or when the giver can't see the occasion itself (see [Occasion fields](#occasion-fields)). | yes: the copy from reveal. Both are `null` when, at reveal, either test hid it. | yes: the copy from reveal |
| `occasion_date`, `reveal_at` | yes | yes | yes |
| `given_at` | `null` | yes | yes |
| `is_bought`, `note` | yes | yes | no |
| `flags` | yes | no | no |
| `needs_attention` | yes | no | no |
| `created_at` | yes | yes | no |

- `giver` is `null` after the giver is deleted. `giver_name` is the giver's name as it was.
- `flags` is an array of flag objects. Each has a `code`, and some codes have more fields:
  - `changed`, with `fields` (the item fields that changed since the giver picked the gift, in the order they first changed; see below)
  - `removed`
  - `moved`, with `list` (the new list's summary, or `null` when the giver can't see the new list)
  - `occasion_moved`
  - `no_longer_shared`
  - `restored`, with `by` (a user summary, or `null` when the giver can't see that user)
  - `recipients_changed`, with `old_recipients` and `new_recipients` (user summaries, or `null` when the giver can't see the list). Both leave out the giver, as `recipients` does, so a giver who joins the list gets no flag on their own gift. Like a `changed` flag, it keeps the first `old_recipients` until the giver answers it, and `new_recipients` is the latest. If the recipients are the ones the giver last saw, the flag goes away.
  - A gift flagged `occasion_moved` or `no_longer_shared` has no other field. A giver is flagged `no_longer_shared` once, when they come to see none of the list's recipients, and not again until they have seen one in between.
- Each entry of a `changed` flag's `fields` is `{field, url?, old?, new?}`. `old` is the value when the giver picked the gift, or when they last answered the flag, and `new` is the value now. A field that changes again keeps its first `old`, and a field that changes back leaves `fields`, and the flag with the last one.

  | `field` | `old` and `new` |
  |---|---|
  | `title`, `notes` | a string, or `null` for no notes |
  | `quantity` | a number |
  | `is_unlimited` | a boolean |
  | `price` | the item price as one value: `{price_minor, top_price_minor, currency}`, or `null` for no price. An item's first price ever is not a change, so `old` is `null` only after the price was cleared. |
  | `primary_image` | none: the entry says only that the primary image changed. A change to another image is not a change. |
  | `store_link_price` | a price as for `price`, with `top_price_minor` always `null`. `url` names the store link. A link's first price is not a change. |
  | `store_link_removed` | none. `url` names the removed store link. Adding a store link is not a change. |

  Every value is a field that the giver sees on the item.
- `needs_attention` is `true` for an open gift that isn't bought and whose reveal is within 14 days, or an open gift flagged `changed` or `removed`. A given gift never needs attention.
- A giver sees a gift's occasion only when both tests pass: it is an occasion of a recipient the giver can see (see [Occasions for a gift](spec.md#occasions-for-a-gift)), and the giver can see the occasion itself. The second test changes something only for the admin: they never see, through a gift, a custom occasion that their access doesn't show them. When either test fails, the giver sees only the reveal date.
- After reveal, the occasion summary stays while the occasion exists. `occasion_name` is the name copied at reveal, so it stays when the occasion is deleted.

`can_*` fields on a gift:

| Field | `true` when |
|---|---|
| `can_edit` | the caller is the gift giver and the gift is open |
| `can_delete` | same as `can_edit` (unclaim) |
| `can_raise_quantity` | `can_edit`, the caller isn't a recipient of the list, and more are still wanted or the item is unlimited |
| `can_acknowledge` | `can_edit`, and the gift has flags |
| `can_restore` | the caller is a gift recipient, or the parent of a gift recipient who is active, and the gift is given |
| `can_add_again` | same as `can_restore` |

### Gift routes

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/gifts` | signed in | The gifts the caller can see: the ones they give, and the given gifts for them or their children. Filters: `?state=`, `?giver_id=` (from `giver`), `?recipient_id=` (given gifts that have this user among `recipients`), `?item_id=`, `?list_id=`, `?occasion_id=`, `?needs_attention=`. A filter never matches a field that the caller doesn't see (see [Collections](#collections)), so `?occasion_id=` never finds a gift whose occasion is hidden from its giver. Sort: `reveal_at`, `given_at`. Giving is `?giver_id=me&state=open`. Given is `?giver_id=me&state=given&sort=-given_at`. Received is `?recipient_id=me&sort=-given_at`, and a parent finds the gifts given to a child with `?recipient_id={child}`. |
| POST | `/items/{id}/gifts` | giver | Claims the item. Body: `quantity` (1 by default), `occasion_id`, `from_label`, `note`, `is_bought`. When `occasion_id` is left out, the server picks the first occasion that `GET /occasions?list_id=` returns in its default order: the soonest one that the giver may pick. 201 with the gift. Codes: 409 `item_fully_claimed`, 409 `already_claimed`, 422 `occasion_not_allowed`, 403 `recipient_cannot_claim`, 403 `user_deactivated` (the caller sees the list only through a deactivated recipient). |
| GET | `/gifts/{id}` | gift giver; gift recipient and their parent after reveal | |
| PATCH | `/gifts/{id}` | gift giver, open gift | Body: `quantity`, `occasion_id`, `from_label` (at most 60 characters), `note`, `is_bought`. A raise while the giver is a recipient is a 403 `quantity_raise_not_allowed`, always, as in [Who a gift is for](spec.md#who-a-gift-is-for). When the giver can see none of the other recipients, any `occasion_id` is a 422 `occasion_locked`, also the gift's own: the server picks the occasion (see [Changes while a gift is open](spec.md#changes-while-a-gift-is-open)). When the new occasion merges this gift into the giver's other open gift on the item (see [Occasions for a gift](spec.md#occasions-for-a-gift)), the response is 200 with the gift that stays, and this gift's address is a 404 from then on. Codes: 409 `item_fully_claimed`, 422 `occasion_not_allowed`, 422 `occasion_locked` (the giver can see none of the other recipients). |
| DELETE | `/gifts/{id}` | gift giver, open gift | Unclaims. The quantity goes back to all givers. 204. Code: 403 `gift_given`. |
| POST | `/gifts/{id}/acknowledge` | gift giver, open gift | Body: `codes` (optional, every flag by default). Clears those flags. Keeping a removed gift is acknowledging `removed`. Leaving `codes` out clears every flag, and `[]` clears none. A code the gift doesn't have is skipped, and a string that is no flag's code is a 422 `invalid_input`. A given gift is a 403 `forbidden`. Clearing `changed` drops its `fields`. 200 with the gift. |
| POST | `/gifts/{id}/restore` | gift recipient, their parent | "I didn't get this". 204 with no body, as in [Restore](spec.md#restore). A gift the caller can't see is a 404, which an open gift is for its recipients, so the answer never says whether a gift is claimed. The gift giver, who sees the gift and isn't a recipient or a recipient's parent, gets a 403 `forbidden`. A parent whose child receives the gift while the child is deactivated sees the gift and gets a 403 `user_deactivated`. |

## Blocked stores

Blocked store fields: `id`, `store`, `created_at`. Only the admin sees them. `can_*` fields: `can_delete`.

| Method | Path | Roles | Notes |
|---|---|---|---|
| GET | `/blocked-stores` | admin | Sort: `store`. |
| POST | `/blocked-stores` | admin | Body: `store` (a host name or a web address). 201. Blocking a store that is blocked changes nothing, and is a 409 `already_blocked`. A client can treat it as success. |
| DELETE | `/blocked-stores/{id}` | admin | 204. |

The list shows only the stores, not the items that link to them, because the admin has no access to content.
