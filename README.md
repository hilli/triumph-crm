# Triumph Polska CRM

A browser-based CRM written in Go, using only the Go standard library.

## Features

- Customer contact details and addresses.
- Motorcycle inventory with VIN, model, year, colour, and price in PLN.
- Customers' existing motorcycles, including other manufacturers.
- Sales contracts linking customers and inventory, with contract numbers,
  dates, prices, deposits, and draft/signed/cancelled statuses.
- Dashboard, search, and forms for creating, editing, and deleting records.

Prices are stored in integer grosz. Deposits cannot exceed the contract price,
and an inventory motorcycle can have only one active (draft or signed)
contract. Cancel a contract before creating a replacement. Records referenced
by other records cannot be deleted until those relationships are removed.

## Run locally

Install Go 1.26.11 or newer. From the repository directory, run in Bash:

```bash
export CRM_USERNAME=admin
read -r -s -p "CRM password: " CRM_PASSWORD
echo
export CRM_PASSWORD
go run .
```

Open <http://127.0.0.1:8080> and enter those credentials in the browser's
authentication prompt. There are no default credentials or sample customer
records. Use a strong, unique password; do not commit it or put it in a shared
shell history.

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `CRM_USERNAME` | Required | Staff login name |
| `CRM_PASSWORD` | Required | Staff login password |
| `CRM_ADDR` | `127.0.0.1:8080` | HTTP listening address |
| `CRM_DATA_FILE` | `data/crm.json` | Persistent data file |
| `CRM_SECURE_COOKIE` | `false` | Set `true` behind an HTTPS reverse proxy |

Create a customer and an inventory motorcycle, then create a sales contract
by selecting both records. Record a customer's current bike separately under
existing motorcycles; it does not need to be a Triumph or part of inventory.

## Development

```sh
go test ./...
go vet ./...
go build -o triumph-crm .
```

The application needs no database server, JavaScript build tools, or Python
dependencies.

## Data and deployment

Data is saved to a local JSON file. Run only **one application process per
data file**; this storage is intended for a small dealership installation,
not multiple replicas. Back up the data file regularly and restrict access to
the server and backups. The file is not encrypted at rest. Local data and
compiled binaries are excluded from Git.

The default listener is loopback-only. For remote use, deploy behind an
**HTTPS reverse proxy** and do not expose plain HTTP: HTTP Basic authentication
does not encrypt credentials. Set `CRM_SECURE_COOKIE=true` for HTTPS deployment
to enable secure form cookies and HTTPS origin checks. Preserve the public
host and origin through the proxy so those checks work. The built-in login is
a single shared staff account, not per-user roles or an audit system. Browser
authentication may remain cached until the browser is closed. Restrict remote
access to trusted staff, and configure rate limiting at the proxy.

Contracts here are sales records, not legally reviewed Polish contract
documents or an electronic-signature service. Before storing real customer
data, establish your GDPR access, retention, backup, and deletion procedures.