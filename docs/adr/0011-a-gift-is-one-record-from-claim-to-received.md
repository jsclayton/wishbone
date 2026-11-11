---
status: accepted
---

# A gift is one record from claim to Received

A **gift** is one record for its whole life. A giver starts it when they claim an item. Until reveal, only the giver sees it. At reveal, Wishbone copies onto the gift what was given (the title, the primary image, and the price) and the occasion's name, and the gift's recipients and their parents can then see it, without the giver's private note and bought mark. Received and Given are two views of the same gifts. Restore doesn't delete the gift: it opens the gift again for its giver, who keeps it for the next occasion or unclaims it. **Claim** is only a verb.

Reveal keeps gifts and doesn't expire them. With expiry, the items would reappear for every giver, and lists would fill with gifts already given. Most claimed items were bought and given. For the rare item that wasn't, a recipient taps "I didn't get this" to restore it. Because a reveal ends an item's life on the list, each gift has its own occasion, chosen by the giver, rather than always revealing at the recipient's next occasion.

We chose one record because the giver's commitment and the recipient's history are the same event at two times. With [one address shaped by the caller](0007-one-address-per-resource-shaped-by-the-caller.md), a resource can show different fields to different callers, so it doesn't need a second record to hide the giver's fields from the recipient.

## Considered Options

- **Two records: a claim, and a gift made at reveal.** Rejected. A gift would be a copy of an item, which blurs item and gift. Restore and deleting a giver would have to keep two records in step.
- **One record named Claim.** Rejected, because "claims you received" reads badly on the recipient's side. Gift fits every step: a giver claims an item, which starts a gift. The gift is given, and the recipient receives it.

## Consequences

- Who can see a gift changes over its life. Before reveal, a gift is a 404 for everyone except its giver. After reveal, its recipients and their parents see it too. Every check on gifts depends on whether the gift is given.
- A gift's life can loop: open, given, and open again after a restore. A giver who still has a gift to hand over keeps their record of it, and the quantity stays claimed, so no other giver buys a second one. If the giver does nothing, the gift is given again at the next occasion.
- The copy at reveal is still needed. An unlimited item stays on the list and can change, and a restored quantity goes back on the item. The gift keeps what was given.
- When a giver is deleted, their given gifts stay for the recipients, with the giver's name as it was. The record can't go with the giver.
- The API has `/gifts/{id}` and `POST /items/{id}/gifts`, not `/claims`. Words such as `can_claim`, `already_claimed`, and "claim state" stay, because they use claim as a verb.
