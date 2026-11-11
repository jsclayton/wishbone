---
status: accepted
---

# Lists are visible through their recipients

A user can have several lists, and a list can have several recipients. Visibility is a matter of users, not of lists. A user sees another user through a shared group or a parent link. A user sees a list when they can see one of its recipients. Lists and items have no visibility settings.

Extended families don't fit one flat household. Two in-laws' households must both see yours without seeing each other. So a group contains users and other groups, and two users share a group when some group contains both, resolved through nesting. There is no separate household or circle type, and there are no per-item audiences. A parent link ([ADR 0006](0006-parents-see-and-edit-their-childrens-lists.md)) also makes users visible to each other.

We kept one rule, so every check on lists, items, counts, and 404s reduces to "can you see a recipient, not counting the admin's access?".

## Considered Options

- **Each list has an audience of groups.** It's more flexible, for example a list for only one side of the family. Rejected, because every list's audience would need upkeep, and recipients would need to agree on it. A later setting can narrow a list's audience. It would be opt-in, so lists that don't use it would see no change.
- **Per-item audiences.** Rejected. Nothing asks for them, and they would add an audience check to every item query. A later opt-in setting can add them in the same way.
- **Groups without nesting.** Flattening keeps the same visibility pairs. Rejected, because the admin would have to add each new user to every group that should contain them, by hand, and a missed group silently hides that user.

## Consequences

- A shared list shows its recipients to everyone who can see the list, as summaries. A summary doesn't make a user visible, so a viewer who can't otherwise see a recipient gets a 404 for that user.
- A list whose recipients are in different groups is seen by the users of all those groups.
- The admin sees every user and group to manage them, but sees lists only through this rule.
