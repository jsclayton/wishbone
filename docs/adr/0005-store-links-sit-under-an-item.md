---
status: accepted
---

# Store links sit under an item instead of being composite items

When a product is sold in several stores, the users who edit a list have one item with several **store links**, not several items grouped together. Gifts, quantity, unlimited, position, and reveal all describe the thing the recipients want, not the store. With separate items, two givers could claim the same product from two stores, and the recipients would get duplicates. The change flags of a gift would also have to work across a group. Store links are interchangeable ways to buy the same product, so a variant (color, size, edition) is a separate item. A gift is of the item and does not record the store. Each store link has its own price. The item also has a price of its own, which is the one givers see: it starts from the first store link's price, is set from a store link at most once, and otherwise changes only when a user who can edit the list edits it, so a refresh of the store links cannot change what givers see. Wishbone does not match products across stores automatically, because a wrong merge is worse than a missed one.

The users who can edit the list are its recipients and their parents ([ADR 0006](0006-parents-see-and-edit-their-childrens-lists.md), [ADR 0009](0009-lists-have-equal-recipients-not-an-owner.md)). A gift is one record from claim to Received ([ADR 0011](0011-a-gift-is-one-record-from-claim-to-received.md)). See [Store links](../spec.md#store-links) in the spec.
