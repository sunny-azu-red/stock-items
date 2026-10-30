# Deployment

The app is a single static binary. Templates, CSS, JavaScript and migrations are embedded. It only needs a reachable Postgres database and a `.env` file, or environment variables.

> There is no authentication. Only expose the app on a trusted home network, never directly to the internet. For remote access, use a VPN (WireGuard, Tailscale) or a reverse proxy that adds authentication.

## Build

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(git describe --always --dirty)" \
  -o bin/pitbox ./cmd/pitbox
```

Cross-compile for a Raspberry Pi or another ARM server with `GOOS=linux GOARCH=arm64`.

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

```bash
docker build -t pitbox --build-arg VERSION=$(git describe --always) .
docker run -d --name pitbox --restart unless-stopped -p 8080:8080 --env-file .env pitbox
```

Inside the container, `127.0.0.1` is the container itself. If Postgres runs on the Docker host, set `DB_HOST` to the host's LAN address, or use `--network host` (then drop `-p`). Docker's `--env-file` keeps quotes as part of the value, so write `DB_PASSWORD=secret`, not `DB_PASSWORD="secret"`.

The image is distroless and runs as a non-root user (~27 MB).

## Upgrades

Stop the service, replace the binary and start it again. New migrations are applied automatically on start. To apply them separately, run `pitbox -migrate-only`.

## Health check

`GET /healthz` returns `{"status":"ok","version":"…"}` (HTTP 200), or `{"status":"unavailable","database":"down"}` with HTTP 503 when the database is unreachable. The distroless image has no shell or curl, so check it from outside the container (for example from your monitoring or reverse proxy).

## Backups

Everything, photos included, lives in the database:

```bash
pg_dump -h <db-host> -U <db-user> -Fc stock_items > stock_items-$(date +%F).dump
# restore: pg_restore -h <db-host> -U <db-user> -d stock_items --clean stock_items-YYYY-MM-DD.dump
```

You can also download the current stock as CSV from the Stock page.

## HTTPS (optional)

The app speaks plain HTTP. Putting it behind Caddy or nginx with a certificate (for example from your router or an internal CA) enables the browser's modern clipboard API. Copying already works over plain HTTP through a fallback. HTTPS would also allow future camera-based barcode scanning.
