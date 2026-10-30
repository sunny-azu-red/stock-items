# Pitbox

A small, private garage app for your vehicles: the parts and fluids on the shelf, the service jobs they need and when they are due, and what to buy before the next one.

It is built for one household on a home network: no login, one binary, one Postgres database.

![Dashboard](docs/screenshots/desktop-dashboard.png)

## Features

- **Stock tagged by vehicle.** An item can fit one vehicle, several (e.g. an O-ring that fits both bikes), or none ("General" stock such as cleaners).
- **Pieces, ml/L and g/kg.** Type `525 ml`, `1.5 L`, `1,5 l` or `400g`. Quantities display the way you think about them (`6x`, `525 ml`, `1.5 L`).
- **Several SKUs per item:** the OEM number, the manufacturer number, one per pack size. Search finds items by any of them, with or without dashes, so a USB barcode scanner works too.
- **Stock view grouped like a shopping note:** per vehicle, then shared items, then general stock. Filter by vehicle (fits any / fits all), category, low stock or expiring. **Copy as text** gives you:
  ```
  KTM 390
  ------------
  1x - OEM Rear Brake Pads
  525 ml - Silkolene 15W50 PRO 4 XP
  ```
  CSV export is available too, with last prices and stock value, plus a CSV of the whole history.
- **Quick −/+** on every row, and a full **history** of every stock change (purchase, use, count, service) with prices and shops. Mistakes can be edited or deleted, and the stock follows.
- **Service kits.** For example, "KTM 390 – Oil service" lists the filter, 1.7 L oil and 2 O-rings. You see at once what is on the shelf and what is missing, and one click puts the shortfall on the shopping list.
- **Service log.** "Do this service" takes the parts from stock, records date, odometer and cost, and updates the vehicle's odometer. A logged service can be edited (parts, labour, odometer…) or deleted; its parts and odometer change follow.
- **Reminders:** kits can repeat every X km and/or Y months. The dashboard shows what is overdue or due soon.
- **Shopping list** with automatic suggestions: items below their minimum, and parts missing for kits. Ticking an entry off records the purchase (partial buys stay on the list), and something new you bought becomes a stock item in one step.
- **Prices from day one:** a new item can be saved with the price and shop you just bought it at, so it immediately has a last price and counts in the stock value and spending.
- **Nothing disappears by accident:** items used in services, kits or purchases, and vehicles with services or kits, are archived instead of deleted, so history and spending stay correct, and they can be restored.
- **Photos** of items (resized and stored in Postgres), best-before dates, storage locations and notes.
- Works well on a phone in the garage, with dark mode and keyboard shortcuts (`/` search, `n` new item).

| Stock grouped by vehicle | Service kit | Phone |
|---|---|---|
| ![Stock](docs/screenshots/desktop-stock.png) | ![Kit](docs/screenshots/desktop-kit.png) | ![Mobile](docs/screenshots/mobile-stock.png) |

## Quick start

Requirements: [Go](https://go.dev/dl/) 1.27.3+ and a PostgreSQL database (tested with 18).

```bash
cp .env.example .env     # then fill in your database credentials
go run ./cmd/pitbox # migrates the database and serves http://0.0.0.0:8080
```

Open `http://<this-machine>:8080` from any device on your network. Start by adding your vehicles, then your items.

### Build a binary

```bash
CGO_ENABLED=0 go build -o bin/pitbox ./cmd/pitbox
./bin/pitbox            # reads .env from the working directory
```

See [docs/deployment.md](docs/deployment.md) for running it as a systemd service or with Docker.

## Configuration

All settings come from environment variables or a `.env` file (real environment variables win):

| Variable | Default | Meaning |
|---|---|---|
| `DB_HOST`, `DB_PORT` | `127.0.0.1`, `5432` | Postgres server |
| `DB_DATABASE` | `stock_items` | Database name |
| `DB_USERNAME`, `DB_PASSWORD` | `postgres`, empty | Credentials |
| `DB_SCHEMA` | `public` | Postgres schema (tests use throwaway schemas) |
| `HOST`, `PORT` | `0.0.0.0`, `8080` | Where the web server listens |
| `APP_CURRENCY` | `EUR` | Label shown next to prices |

`.env` is git-ignored. Never commit it.

## Development

```bash
scripts/check.sh                     # gofmt, go vet, staticcheck, unit + integration tests
cd e2e && npm ci && npx playwright test   # end-to-end tests (desktop + phone)
```

The integration and e2e tests use throwaway schemas (`test_*`, `e2e_*`) in the configured database and never touch your data. More in [docs/development.md](docs/development.md).

## Documentation

- [User guide](docs/user-guide.md): how to use the app day to day
- [Architecture](docs/architecture.md): how the code is organised
- [Data model](docs/data-model.md): tables and rules
- [Development](docs/development.md): setup, tests, adding features
- [Deployment](docs/deployment.md): systemd, Docker, backups
- [Conventions](docs/conventions.md): rules for contributors and coding agents (see also [AGENTS.md](AGENTS.md))

## License

MIT. See [LICENSE](LICENSE). Icons from [Lucide](https://lucide.dev) (ISC), [htmx](https://htmx.org) (BSD-2-Clause).
