---
status: accepted
---

# One address per resource, shaped by the caller

Every resource has one canonical address, such as `/items/{id}` or `/lists/{id}`. The server decides what the caller can do, and which fields the caller gets, from the caller's relationship to the resource. A field that the caller may not see is absent from the response, not `null`. Every full resource has flat `can_*` booleans that tell the client which actions it can take.

We chose this because resources have many relationships: a parent edits a child's lists, recipients share a list, and a mobile app caches resources by ID. With one address for each viewpoint, the route table would grow with every relationship, and a client would cache one resource in several shapes.

## Considered Options

- **One tree of routes for each viewpoint.** For example, an item at one address for a recipient, and in another collection for a giver. Each route is simple to authorize, but the route table grows with each new relationship. A parent's view of a child's list would need a new tree.
- **Clients derive permissions from relationship fields only, with no `can_*` fields.** The rules would live in every client, and a client would sometimes need more requests to know which buttons to show. Rejected, because the response that says what the caller can do saves clients from knowing every rule and from making more requests.
- **A nested `can` object.** The maintainer chose flat `can_*` fields instead.

## Consequences

- One handler serves every role, so each route must check the caller's relationship to the resource before it builds the response.
- A hidden field is left out, and an empty field is `null`, so a client can't confuse the two.
- A resource that the caller can't see is a 404, the same as a resource that doesn't exist, so a status never shows that a hidden resource exists.
- `me` is a reserved user ID, not a second tree of routes.
