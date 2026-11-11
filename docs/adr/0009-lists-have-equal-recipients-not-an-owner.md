---
status: accepted
---

# Lists have equal recipients, not an owner

A list has one or more **recipients**, and they are equal: none of them owns the list. A couple shares a list by consent, and neither partner is the other's guest. Every user also keeps one **primary list** with only themselves as its recipient, so "my list is mine" stays true, and the sharing rules apply only to extra lists.

## Considered Options

- **One owner, plus co-recipients.** Someone always has the final say, for example on deleting the list. Rejected, because for a couples list, one partner would own "our list" and the other wouldn't. A list with no owner also survives any one recipient leaving.
- **One recipient for each list.** A couple keeps two lists. Rejected, because it drops the shared list.

## Consequences

- An action that affects every recipient needs its own rule. A recipient deletes a list only when they are its only recipient. While others remain, they leave instead.
- Becoming a recipient hides the list's claim state from the user, so a user joins a list only by creating it or by accepting an invitation. A parent can make that choice for their child.
- There is no `owner_id`. A list has `recipients`, and clients filter lists with `?recipient_id=`.
- "Recipient" is a role on a list. An editor who isn't a recipient isn't part of the model, but adding it later is an additive change.
