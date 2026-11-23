# Deployment

The app is a single static binary. Templates, CSS, JavaScript and migrations are embedded. It needs a reachable Postgres database, a `.env` file or environment variables, and, for photos, the libvips command line tools (`vips`, `vipsheader`) on `PATH`. The Docker image includes them; nothing to install there.

> There is no authentication. Only expose the app on a trusted home network, never directly to the internet. For remote access, use a VPN (WireGuard, Tailscale) or a reverse proxy that adds authentication.

## Updates and versions

Push to `master`. CI tests the commit (unit, integration and browser tests, lint, the Docker image) and only if all of that passes publishes the image `ghcr.io/sunny-azu-red/pitbox` as `latest` and `sha-<commit>`. Then re-pull in Portainer (see [Portainer](#portainer)). A red run publishes nothing, so `latest` stays on the last good commit.

To stay on a version, set `PITBOX_TAG=sha-<commit>`. Going back to an older one is only safe if no migration came in between; otherwise restore a [backup](#backups).

Tags are optional. If you want a named version, push one (`git tag -a v1.2.3 -m v1.2.3 && git push origin v1.2.3`): CI publishes `1.2.3` and `1.2` images (never `latest`) and a [GitHub Release](https://github.com/sunny-azu-red/stock-items/releases) with static `linux-amd64` and `linux-arm64` binaries and `SHA256SUMS`, for running without Docker.

## Build

To build a binary yourself:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty)" \
  -o bin/pitbox ./cmd/pitbox
```

Pitbox runs on an amd64 Linux server (here, an LXC container on a Proxmox host). To build on another machine, such as a Mac, set `GOOS=linux GOARCH=amd64`. Build from a clean checkout: a version ending in `-dirty` counts as a development build, which refuses the `public` schema.

## Run with systemd

```bash
sudo apt install --no-install-recommends libvips-tools libheif-plugin-libde265   # photos (HEIC needs the second)
sudo useradd --system --home /opt/pitbox pitbox
sudo mkdir -p /opt/pitbox
sudo cp bin/pitbox /opt/pitbox/   # or a tagged release's binary, renamed to pitbox
sudo cp .env.example /opt/pitbox/.env && sudo chmod 600 /opt/pitbox/.env
sudo chown -R pitbox: /opt/pitbox
```

Then edit `/opt/pitbox/.env`: the database credentials (the example already uses `DB_SCHEMA=public`). Don't copy your development `.env`, which uses `dev`.

`/etc/systemd/system/pitbox.service`:

```ini
[Unit]
Description=Pitbox
# Drop postgresql.service from both lines if the database is on another machine.
After=network-online.target postgresql.service
Wants=network-online.target postgresql.service

[Service]
User=pitbox
WorkingDirectory=/opt/pitbox
ExecStart=/opt/pitbox/pitbox
Restart=on-failure
# The app gives up when the database is not reachable at start; retry slowly
# rather than hitting systemd's start limit while Postgres boots.
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pitbox
journalctl -u pitbox -f
```

## Run with Docker

The image is Debian 13 slim (`debian:trixie-slim`) upgraded to Debian's current packages at build time, with `libvips-tools` (and libheif's HEVC decoder for HEIC photos), `ca-certificates` and `tzdata`, and runs as a non-root user (uid 65532). It is about 360 MB unpacked (92 MB to download), almost all of it libvips and its libraries; the earlier distroless image without photo tools was 29 MB. It has a `HEALTHCHECK` (the binary runs `/pitbox -healthcheck`), so `docker ps` and Portainer show it as *healthy* once it has reached the database.

CI publishes it as `ghcr.io/sunny-azu-red/pitbox` for `linux/amd64` only (see [Updates and versions](#updates-and-versions)): `latest` for the newest `master` that passed CI, `sha-<commit>` for every push, and `1.2.3` and `1.2` for an optional `v1.2.3` tag. Each image carries an SBOM and provenance attestation and is scanned for known vulnerabilities. [`compose.yaml`](../compose.yaml) runs it with a read-only root filesystem (photos are processed in a `tmpfs` at `/tmp`) and `no-new-privileges`.

### Portainer

1. **Stacks → Add stack**, name it `pitbox`.
2. Choose one:
   - **Repository**: URL `https://github.com/sunny-azu-red/stock-items`, reference `refs/heads/master`, compose path `compose.yaml`. Portainer reads the file from GitHub, so changes to it arrive with the next update.
   - **Web editor**: paste the contents of `compose.yaml`.
3. Under **Environment variables**, add:

   | Variable | Example | |
   |---|---|---|
   | `DB_HOST` | `192.168.1.10` | required: the Postgres server's LAN address |
   | `DB_PASSWORD` | | required |
   | `DB_PORT`, `DB_DATABASE`, `DB_SCHEMA`, `DB_USERNAME` | | default `5432`, `stock_items`, `public`, `postgres` |
   | `APP_CURRENCY` | `LEI` | default `EUR`; up to 10 characters |
   | `APP_DISTANCE` | `mi` | default `km`; up to 10 characters, shown as typed. Only a label: odometer readings and intervals are kept as entered, not converted |
   | `APP_SKU_SEARCH_URL` | `https://duckduckgo.com/?q={q}` | default `https://www.google.com/search?q={q}`. Part number links put the brand and number (or just the number) in place of `{q}`, wherever it is (`https://example.com/search?q={q}&lang=de`). A full `http(s)://` URL with exactly one `{q}`, or the app won't start (see [Upgrades](#upgrades)) |
   | `APP_SKU_SEARCH_PREFIX` | `site:de` | added to every part number search; none if unset |
   | `APP_ALLOWED_HOSTS` | `pitbox.lan,192.168.1.5` | the names and IPs you open Pitbox by (ports ignored); any other gets 421, so a web page whose name was pointed at your server (DNS rebinding) can't use it. Unset: any name. `localhost` always works |
   | `TZ` | `Europe/Berlin` | the app's zone: when "today" starts (form dates, date checks, expiring, reminders, spending, "Deleted today"), the day a service counts as logged on, and the times pages show. Default the Docker host's zone (`compose.yaml` mounts its `/etc/localtime`); empty or unknown also means that. The database's own zone doesn't matter: the app's sessions are in UTC and timestamps are stored in UTC |
   | `PITBOX_PORT` | `3002` | port on the Docker host; default `3002` |
   | `PITBOX_TAG` | `sha-7e8f96f` | default `latest`, the newest `master` that passed CI; pin `sha-<commit>` (or a tagged version) to stay on it |

   Type values without quotes.
4. **Deploy the stack** and open `http://<docker-host>:3002` (or the `PITBOX_PORT` you set). Don't add `PORT`: the compose file doesn't pass it to the container, so it changes nothing.

**Updating:** after a push to `master` whose CI passed, open the stack, click **Update the stack** and turn on **Re-pull image and redeploy**. New migrations run on start, so take a [backup](#backups) first when the update adds a file to `internal/db/migrations`. Going back to an older tag is only safe if no migration came in between; otherwise restore the backup.

**The image is private?** GitHub makes a new package private on its first publish. Set `pitbox` to public under the package's settings on GitHub, or add `ghcr.io` with a personal access token (scope `read:packages`) under **Registries** in Portainer.

**Database access:** the container connects from the Docker host's address, or from a Docker network (`172.16.0.0/12`) when Postgres runs on the Docker host itself. Postgres has to listen on that interface (`listen_addresses`) and allow that address in `pg_hba.conf`. Inside the container `127.0.0.1` is the container itself, so never use it for `DB_HOST`.

### Docker Compose or plain Docker

With a `.env` next to `compose.yaml` (Compose reads it for the `${…}` values). Three things differ from the development `.env`:

- `DB_SCHEMA` must be `public`, as in `.env.example` (or left out: that is the compose default), not a development `.env`'s `dev`.
- `DB_HOST` must be the Postgres server's LAN address, not `127.0.0.1` as in `.env.example`: inside the container that is the container itself.
- Compose ignores `HOST` and `PORT`; the app listens on 8080 inside the container, and `PITBOX_PORT` (default 3002) picks the port on the Docker host. Plain `docker run --env-file` passes them through, so leave them out of that file (or match `-p` to `PORT`).

```bash
docker compose up -d                       # or: docker compose pull && docker compose up -d
```

To build the image yourself instead of pulling it:

```bash
docker build -t pitbox --build-arg VERSION=$(git describe --tags --always --dirty) --build-arg APT_REFRESH=$(date +%s) .
docker run -d --name pitbox --restart unless-stopped -p 3002:8080 --env-file .env \
  --read-only --tmpfs /tmp --security-opt no-new-privileges \
  -v /etc/localtime:/etc/localtime:ro pitbox
```

`APT_REFRESH` (any new value) makes the `apt-get upgrade` layer run again instead of coming from the build cache, so the image gets Debian's current security fixes, as CI does on every build. The image builds for the machine's own platform; building for another (`--platform`) needs QEMU, since the runtime stage installs packages.

Docker's `--env-file` keeps quotes as part of the value, so write `DB_PASSWORD=secret`, not `DB_PASSWORD="secret"`. Compose's `.env` handles quotes normally.

## Upgrades

New migrations are applied automatically on start; to apply them separately, run `pitbox -migrate-only`. With systemd, stop the service, replace the binary and start it again. With Docker, see [Portainer](#portainer).

**The squash.** Every migration up to 21 Nov 2026 is in `00001_init.sql` (goose version 1); later ones follow it from 00002. **Never start an image from before `a756c20`:** it would run its old migrations 00002–00022 again on the existing tables, and those before the first failure would stay applied. To go back to such an image, restore the backup taken before the move (`stock_items-public-20261121-122205.dump` in the sync backups) with it.

**00003 (vehicle warranty).** It adds two empty warranty columns, so nothing changes until a warranty is entered, with one exception: a kit with an intermediate schedule whose Intermediate months is empty used to count as under warranty (the full service keeping its own deadline), and now the vehicle's warranty decides. Right after deploying it, open each vehicle under warranty and enter its end (Under warranty, Warranty ends on); until then its kits count as alternating.

**00004 (vehicle required fields).** A vehicle's odometer, purchase date and purchase reading become required (`NOT NULL`). Nothing is filled in: if a vehicle, or a deleted one in the trash, lacks any of them, the migration stops, names it and changes nothing (the app doesn't start). Before deploying, check that every vehicle has them; fill in any that doesn't on its edit form (restore a deleted one first, or empty it from the trash). The form now also requires make, model, year and VIN, and a warranty's end date: open each vehicle and save it once to see whether anything is missing.

## Health check

The running build's version shows when you hover the Pitbox logo at the top of the sidebar, and on any device (a phone too) at `/healthz`. `GET /healthz` returns `{"status":"ok","version":"…"}` (HTTP 200), or `{"status":"unavailable","database":"down"}` with HTTP 503 when the database is unreachable. `pitbox -healthcheck` asks the server on `HOST`:`PORT` for it and exits 0 or 1. The Docker image uses it, since the image has no curl. Docker only marks an unhealthy container; it doesn't restart it.

## Backups

Everything, photos and the trash included, lives in the database:

```bash
pg_dump -h <db-host> -U <db-user> -Fc --no-owner stock_items > stock_items-$(date +%F).dump
# restore: pg_restore -h <db-host> -U <db-user> -d stock_items --clean --no-owner stock_items-YYYY-MM-DD.dump
```

You can also download the current stock as CSV from the Stock page.

**A backup from before 2026-11-21** holds goose versions 0–22 and the old `reorder_columns`. `sync_schema.sh --from-backup` refuses it for `public` (it has migrations `public` doesn't). Restore it with `pg_restore` as above, with the stack stopped, then run in one transaction on `public`: `CREATE OR REPLACE FUNCTION` with the `reorder_columns` from `00001_init.sql`, and `DELETE FROM goose_db_version WHERE version_id > 1`. Only nightly backups up to 2026-12-05 are that old (they are kept 14 days).

## HTTPS (optional)

The app speaks plain HTTP. Putting it behind Caddy or nginx with a certificate (for example from your router or an internal CA) enables the browser's modern clipboard API. Copying works over plain HTTP through a fallback in most browsers; where it doesn't (Safari), the text opens to copy by hand. HTTPS would also allow future camera-based barcode scanning.
