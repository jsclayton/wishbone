---
status: accepted
---

# Migrate in place, with an automatic backup

The database changes in place: a goose migration runs when `serve` starts. Before it applies any pending migration, `serve` writes a snapshot of the database and images. If the snapshot fails, nothing is migrated. To go back, the operator restores the snapshot and runs the previous image. A version guard stops an image from starting on a database that is newer than it knows, or older than it supports.

One operator runs one instance with a few dozen users. So the plan is the smallest one that keeps the data safe.

## Considered Options

- **Write a new database from the old one, with a one-off command.** Rejected. The old files would stay untouched, but each release would be a manual procedure with a new data dir. The automatic backup gives the same safety for every release.
- **Export and import.** Rejected. The export format is a third schema to design and test, and the mapping work is the same. Each release would also be a manual procedure.
- **Expand, then contract.** Rejected. A release would add the new tables early and keep them in sync. That is a lot of machinery for one instance.
- **Down migrations.** Rejected. A migration can drop or reshape data that doesn't fit the older schema, so a down migration can't be lossless. The snapshot is the way back.

## Consequences

- A restore loses every write made after the snapshot. The default is to roll forward, and to restore only for a serious problem.
- One baseline migration creates the whole schema. It is never edited, and every schema change is a new migration. The version guard refuses a database that has migrations but not the baseline.
- The server never deletes a snapshot named for a move from a version below the baseline's, because it can be the only copy of that data. The operator deletes it by hand.
