---
status: accepted
---

# Parents see and edit their children's lists

A parent sees and edits all of their child's lists directly, because the parent link grants it. This takes friction away from parents who help with their kids' lists. A parent link therefore grants visibility as well as groups ([ADR 0010](0010-lists-are-visible-through-their-recipients.md)): groups are not the only thing that makes a list visible.

## Considered Options

- **The parent link covers only the child's account (sign-in, sessions, profile, occasions), and list roles alone grant list editing.** Each permission would have one source. Rejected for now, because a parent would need a separate role on each of the child's lists. This can change. If list roles such as an editor are added later, a parent link could set up an editor role on the child's lists, instead of granting editing directly.

## Consequences

- A permission check for editing or seeing a list must also check the parent links.
- Lists are first-class resources, so a query for the lists a user can edit includes their children's lists.
- Only the admin can link a parent and a child, and the admin can't have parents. Otherwise a parent could sign in as the admin.
