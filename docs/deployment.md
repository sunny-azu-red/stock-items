# Deployment

The app is a single static binary. Templates, CSS, JavaScript and migrations are embedded. It only needs a reachable Postgres database and a `.env` file, or environment variables.

> There is no authentication. Only expose the app on a trusted home network, never directly to the internet. For remote access, use a VPN (WireGuard, Tailscale) or a reverse proxy that adds authentication.

## Build

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(git describe --always --dirty)" \
  -o bin/stock-items ./cmd/stock-items
```

Cross-compile for a Raspberry Pi or another ARM server with `GOOS=linux GOARCH=arm64`.

## Run with systemd

```bash
sudo useradd --system --home /opt/stock-items stock-items
sudo mkdir -p /opt/stock-items
sudo cp bin/stock-items /opt/stock-items/
sudo cp .env /opt/stock-items/.env && sudo chmod 600 /opt/stock-items/.env
sudo chown -R stock-items: /opt/stock-items
```

`/etc/systemd/system/stock-items.service`:

```ini
[Unit]
Description=Stock Items
After=network-online.target
Wants=network-online.target

[Service]
User=stock-items
WorkingDirectory=/opt/stock-items
ExecStart=/opt/stock-items/stock-items
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now stock-items
journalctl -u stock-items -f
```

## Run with Docker

```bash
docker build -t stock-items --build-arg VERSION=$(git describe --always) .
docker run -d --name stock-items --restart unless-stopped -p 8080:8080 --env-file .env stock-items
```

The image is distroless and runs as a non-root user (~27 MB).

## Upgrades

Stop the service, replace the binary and start it again. New migrations are applied automatically on start. To apply them separately, run `stock-items -migrate-only`.

## Health check

`GET /healthz` returns `{"status":"ok"}` (HTTP 200), or HTTP 503 when the database is unreachable.

## Backups

Everything, photos included, lives in the database:

```bash
pg_dump -h <db-host> -U <db-user> -Fc stock_items > stock_items-$(date +%F).dump
# restore: pg_restore -h <db-host> -U <db-user> -d stock_items --clean stock_items-YYYY-MM-DD.dump
```

You can also download the current stock as CSV from the Stock page.

## HTTPS (optional)

The app speaks plain HTTP. Putting it behind Caddy or nginx with a certificate (for example from your router or an internal CA) enables the browser's modern clipboard API. Copying already works over plain HTTP through a fallback. HTTPS would also allow future camera-based barcode scanning.
