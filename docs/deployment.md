# Deployment

The app is a single static binary. Templates, CSS, JavaScript and migrations are embedded. It only needs a reachable Postgres database and a `.env` file, or environment variables.

> There is no authentication. Only expose the app on a trusted home network, never directly to the internet. For remote access, use a VPN (WireGuard, Tailscale) or a reverse proxy that adds authentication.

## Build

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty)" \
  -o bin/pitbox ./cmd/pitbox
```

Pitbox runs on an amd64 Linux server (here, an LXC container on a Proxmox host). To build on another machine, such as a Mac, set `GOOS=linux GOARCH=amd64`.

## Run with systemd

```bash
sudo useradd --system --home /opt/pitbox pitbox
sudo mkdir -p /opt/pitbox
sudo cp bin/pitbox /opt/pitbox/
sudo cp .env /opt/pitbox/.env && sudo chmod 600 /opt/pitbox/.env
sudo chown -R pitbox: /opt/pitbox
```

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

The image is distroless and runs as a non-root user. It has a `HEALTHCHECK` (the binary runs `/pitbox -healthcheck`), so `docker ps` and Portainer show it as *healthy* once it has reached the database.

CI publishes it as `ghcr.io/sunny-azu-red/pitbox` for `linux/amd64` only: `latest` for every push to `master`, `sha-<commit>` for every push, and `1.2.3` and `1.2` for a `v1.2.3` tag. [`compose.yaml`](../compose.yaml) runs it.

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
   | `APP_SKU_SEARCH_URL` | `https://duckduckgo.com/?q={q}` | default `https://www.google.com/search?q={q}`. Part number links put the number in place of `{q}`, wherever it is (`https://example.com/search?q={q}&lang=de`). A full `http(s)://` URL with exactly one `{q}`, or the app won't start. A URL ending in `=` (the number used to be appended) is refused too, with the fixed value suggested: add `{q}` at the end |
   | `APP_SKU_SEARCH_PREFIX` | `site:de` | added to every part number search; none if unset |
   | `TZ` | `Europe/Berlin` | the app's zone: when "today" starts (form dates, date checks, expiring, reminders, spending, "Deleted today"), the day a service counts as logged on, and the times pages show. Default the Docker host's zone (`compose.yaml` mounts its `/etc/localtime`); empty or unknown also means that. The database's own zone doesn't matter: the app's sessions are in UTC and timestamps are stored in UTC |
   | `PITBOX_PORT` | `8080` | port on the Docker host |
   | `PITBOX_TAG` | `sha-7e8f96f` | default `latest`; pin a tag to stay on a version |

   Type values without quotes.
4. **Deploy the stack** and open `http://<docker-host>:8080`.

**Updating:** open the stack, click **Update the stack** and turn on **Re-pull image and redeploy**. New migrations run on start, so take a [backup](#backups) first when the update adds a file to `internal/db/migrations`. Going back to an older tag is only safe if no migration came in between; otherwise restore the backup.

**The image is private?** GitHub makes a new package private on its first publish. Set `pitbox` to public under the package's settings on GitHub, or add `ghcr.io` with a personal access token (scope `read:packages`) under **Registries** in Portainer.

**Database access:** the container connects from the Docker host's address, or from a Docker network (`172.16.0.0/12`) when Postgres runs on the Docker host itself. Postgres has to listen on that interface (`listen_addresses`) and allow that address in `pg_hba.conf`. Inside the container `127.0.0.1` is the container itself, so never use it for `DB_HOST`.

### Docker Compose or plain Docker

With a `.env` next to `compose.yaml` (Compose reads it for the `${…}` values). Two things differ from running the binary with the same file:

- `DB_HOST` must be the Postgres server's LAN address, not `127.0.0.1` as in `.env.example`: inside the container that is the container itself.
- Compose ignores `HOST` and `PORT`; the app listens on 8080 inside the container, and `PITBOX_PORT` (default 8080) picks the port on the Docker host. Plain `docker run --env-file` passes them through, so leave them out of that file (or match `-p` to `PORT`).

```bash
docker compose up -d                       # or: docker compose pull && docker compose up -d
```

To build the image yourself instead of pulling it:

```bash
docker build -t pitbox --build-arg VERSION=$(git describe --tags --always --dirty) .
docker run -d --name pitbox --restart unless-stopped -p 8080:8080 --env-file .env \
  -v /etc/localtime:/etc/localtime:ro pitbox
```

Docker's `--env-file` keeps quotes as part of the value, so write `DB_PASSWORD=secret`, not `DB_PASSWORD="secret"`. Compose's `.env` handles quotes normally.

## Upgrades

New migrations are applied automatically on start; to apply them separately, run `pitbox -migrate-only`. With systemd, stop the service, replace the binary and start it again. With Docker, see [Portainer](#portainer).

**`APP_SKU_SEARCH_URL` now needs `{q}`.** Older versions appended the part number to a URL ending in `=`; this one puts it in place of `{q}` and refuses to start without one. If you set it (in `.env` or the stack's variables), add `{q}` at the end, e.g. `APP_SKU_SEARCH_URL=https://www.google.com/search?q={q}`. The start-up error says the same. Left unset, the new default already has it.

## Health check

`GET /healthz` returns `{"status":"ok","version":"…"}` (HTTP 200), or `{"status":"unavailable","database":"down"}` with HTTP 503 when the database is unreachable. `pitbox -healthcheck` asks the server on `HOST`:`PORT` for it and exits 0 or 1. The Docker image uses it, since the image has no shell or curl. Docker only marks an unhealthy container; it doesn't restart it.

## Backups

Everything, photos and Recently deleted included, lives in the database:

```bash
pg_dump -h <db-host> -U <db-user> -Fc stock_items > stock_items-$(date +%F).dump
# restore: pg_restore -h <db-host> -U <db-user> -d stock_items --clean stock_items-YYYY-MM-DD.dump
```

You can also download the current stock as CSV from the Stock page.

## HTTPS (optional)

The app speaks plain HTTP. Putting it behind Caddy or nginx with a certificate (for example from your router or an internal CA) enables the browser's modern clipboard API. Copying works over plain HTTP through a fallback in most browsers; where it doesn't (Safari), the text opens to copy by hand. HTTPS would also allow future camera-based barcode scanning.
