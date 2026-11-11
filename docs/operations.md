# Operations

How to back up, restore, and upgrade a Wishbone instance that runs from the [compose example](../docker-compose.yml). The commands assume the service is called `wishbone`, as in that file, and that you run them in the folder that holds it.

## Where the data is

Everything lives in the volume that the compose file mounts at `/data`:

| Path | What it is |
|---|---|
| `wishbone.db` | The SQLite database. While the server runs, it also has `wishbone.db-wal` and `wishbone.db-shm` beside it. |
| `images/` | Item images and avatars. |
| `backups/` | Snapshots that the server writes before a migration. A snapshot that you make into `/data/backups/` is kept here too. |

The image is distroless: it has no shell, so you can't `docker compose exec wishbone sh`. The `wishbone` program is at `/app/wishbone`, and the commands below run it with `docker compose exec`.

## Back up

`wishbone backup <path>` writes a snapshot of the database and the images while the server runs. Run it in the running container:

```bash
docker compose exec wishbone /app/wishbone backup /data/backups/2026-12-01
docker compose cp wishbone:/data/backups/2026-12-01 ./
```

The first command writes the snapshot into the volume. The second copies it to the folder you are in. The target must not exist. The command creates it and writes:

- `wishbone.db`: a consistent copy of the database.
- `images/`: a copy of the images.

It prints what it wrote, and exits non-zero if it fails. A failed run removes its partial snapshot. If the database refers to an image that is missing, the command keeps the snapshot, lists the missing images, and exits non-zero.

A snapshot in `/data/backups/` uses space on the volume, so delete the old ones when you have copied them elsewhere.

The server doesn't schedule backups. To run one every night, call the command from cron on the host. `-T` stops compose from asking for a terminal, and cron needs `%` written as `\%`:

```cron
0 3 * * * cd /srv/wishbone && docker compose exec -T wishbone /app/wishbone backup /data/backups/$(date +\%F) && docker compose cp wishbone:/data/backups/$(date +\%F) /mnt/backups/wishbone/
```

A snapshot in the same volume doesn't protect you from losing the disk. Copy it to another machine or disk. You can also back up the Docker volume itself with your usual host tools, but copy `wishbone.db`, its `-wal` and `-shm` files, and `images/` together, with the server stopped. A `wishbone backup` snapshot is safe to take while the server runs.

## Restore

A restore puts a snapshot back in place of the current data. It loses every change made after the snapshot. Do it for a serious problem, such as a bad upgrade or a lost disk. For a small mistake, fix it in the app.

The distroless image can't copy files, so use a throwaway container that mounts the volume. These steps put the snapshot in `./2026-12-01` back into the volume:

1. Stop the server:

   ```bash
   docker compose stop wishbone
   ```

2. Replace the data. `--volumes-from wishbone` gives the throwaway container the same `/data` volume as the stopped `wishbone` container, so you don't need the volume's name. The command moves the old files into a new `before-restore-<time>/` folder in the volume, so nothing is lost, even after several restores, and copies the snapshot in:

   ```bash
   docker run --rm --volumes-from wishbone -v "$PWD/2026-12-01":/snapshot:ro alpine sh -c '
     old=/data/before-restore-$(date +%s) &&
     mkdir -p "$old" &&
     for f in wishbone.db wishbone.db-wal wishbone.db-shm images; do
       [ -e "/data/$f" ] && mv "/data/$f" "$old"/
     done
     cp -a /snapshot/wishbone.db /snapshot/images /data/ &&
     chown -R 65532:65532 /data/wishbone.db /data/images'
   ```

   To restore a snapshot that is already in the volume, such as one that the server wrote before a migration, leave out the `-v ...:/snapshot:ro` option and copy from `/data/backups/<name>` instead of `/snapshot`.

3. Start the server:

   ```bash
   docker compose up -d
   ```

A stale `wishbone.db-wal` file corrupts a restored database, so the command moves all three database files, not only `wishbone.db`. The server runs as user 65532 (the distroless `nonroot` user), and the `chown` lets it write the restored files.

When you restore a snapshot from an older release, start that release's image, or the current one: the server migrates the restored database forward, and takes a snapshot first (see [Automatic snapshots](#automatic-snapshots)).

## Upgrade

Back up, then pull the new image and restart:

```bash
docker compose exec wishbone /app/wishbone backup /data/backups/before-upgrade-$(date +%F)
docker compose pull
docker compose up -d
```

When the server starts, it checks the database ([Version guard](#version-guard)), writes a snapshot if a migration is waiting ([Automatic snapshots](#automatic-snapshots)), and applies the migrations. A migration can't be undone. To go back to an older release, restore a snapshot that was made before the migration, and run the older image. The README says [what each kind of release means](../README.md#versions-and-upgrades).

## Automatic snapshots

When `serve` starts and a migration is waiting, the server writes a snapshot first, and then migrates. A new, empty data folder gets one too, because all its migrations are waiting.

- **Where:** `backups/pre-migrate-<from>-to-<to>-<UTC time>/` in the volume, for example `backups/pre-migrate-20261110030443-to-20261201000000-20261201T120000Z/`. `<from>` and `<to>` are migration versions. It holds the same `wishbone.db` and `images/` as `wishbone backup` writes, so the [restore](#restore) steps apply to it.
- **When it fails:** if the snapshot fails, for example because the disk is full, the server removes it, applies no migration, and exits non-zero. Fix the cause and start the server again. The server never migrates a database without a snapshot.
- **No migration waiting:** a restart on a current database writes no snapshot.
- **How many it keeps:** the 3 newest. After a new snapshot, the server deletes the older automatic ones. It never deletes any other folder in `backups/`, so your own snapshots stay until you remove them. If a deletion fails, the server logs an error and still migrates.
- **Space:** each snapshot is a full copy of the database and the images. If the volume is small, check `backups/` now and then.

To list the snapshots, use a throwaway container:

```bash
docker run --rm --volumes-from wishbone:ro alpine ls /data/backups
```

## Version guard

Every command that opens the database checks its version first. The server refuses to start in two cases, and it says which one in its log:

- **The database is newer than the server.** A newer release migrated it, and an older image doesn't know its tables. Run a newer image, or [restore](#restore) a snapshot that this release can read.
- **The database is older than this release supports.** It has migrations, but not the first one that this release is built on (the baseline migration, `20261110030443_baseline.sql`). Wishbone can't upgrade such a database. Keep running the release that wrote it, or start a new instance with an empty data folder. A new, empty database is never refused.

A refused start changes nothing: the server writes no snapshot and applies no migration. The commands other than `serve` (`users`, `login-link`, and so on) also refuse when a migration is waiting, and tell you to start the server first. Only `backup` still works then, because it only reads.

## Commands in the container

These run with `docker compose exec wishbone /app/wishbone <command>`:

| Command | What it does |
|---|---|
| `users` | Lists the users, with their IDs. |
| `login-link <user id>` | Prints a login link that works once and expires after 7 days. It is the way back in for a person who lost their sign-in. |
| `set-admin <user id>` | Makes a user the admin. The old admin becomes a normal user. |
| `delete-user <user id>` | Deletes a user for good, after it shows what it will do and asks for the user's name. |
| `backup <path>` | Writes a snapshot (see [Back up](#back-up)). |
| `healthcheck` | Exits non-zero when the server doesn't answer. The compose file uses it. |
| `version` | Prints the version. |

The [spec](spec.md#cli) describes each command in full.
