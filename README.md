# keenetic-policy-switcher

A local web app for a home network: it lists the devices registered on a Keenetic
router and lets you change the access policy (connection policy / PBR) of each
one through the router's RCI API. A single self-contained Go binary — server and
frontend are compiled into one file.

> ⚠️ **Unofficial project.** Not affiliated with or endorsed by Keenetic. Uses
> the router's undocumented API (RCI); behavior depends on your KeeneticOS
> version. Use at your own risk.

## ⚠️ Security

The service holds **full administrative access to your router** (the admin
login and password). Run it only inside a trusted home network or on
`127.0.0.1`. **Never expose its port to the internet** — neither through port
forwarding on the router nor via a public cloud host. Remote access over KeenDNS
is not supported: the router does not return the authentication headers when
reached through an external domain.

## Verified on

- Router: **Keenetic Giga (KN-1010)**
- Firmware: **KeeneticOS 4.3.6.3** (release `4.03.C.6.3-9`)

The exact RCI paths and response formats captured from this router are
documented in [`docs/api-notes.md`](docs/api-notes.md). Other firmware versions
may differ.

## Running

Requires Go (see `go.mod` for the version).

```sh
cp .env.example .env
# edit .env: KEENETIC_HOST, KEENETIC_LOGIN, KEENETIC_PASSWORD

go build -o keenetic-policy-switcher ./cmd/keenetic-policy-switcher
./keenetic-policy-switcher
```

Listens on `:8080` by default. Open `http://<machine-address>:8080` from a phone
or laptop on the same network.

### With Docker

```sh
cp .env.example .env
docker compose up --build
```

`docker-compose.yml` publishes the port on all host interfaces so a phone on the
LAN can reach it — keep the host itself inside a trusted network, not behind a
port forward from the internet.

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `KEENETIC_HOST` | yes | Router base URL, e.g. `http://192.168.1.1` |
| `KEENETIC_LOGIN` | yes | Router admin login |
| `KEENETIC_PASSWORD` | yes | Router admin password |
| `LISTEN_ADDR` | no (default `:8080`) | Address and port the app listens on |
| `REQUEST_TIMEOUT` | no (default `10s`) | Timeout for requests to the router |

Variables are read from the environment (`os.Getenv`). For local development you
can put them in `.env`, which is loaded softly via `godotenv`: a missing file is
not an error (required for Docker, where the image contains no `.env`), and real
environment variables take precedence over the file. The required variables are
validated at startup — if any is missing, the app exits with an error naming it.
`.env` is listed in both `.gitignore` and `.dockerignore` and is never committed;
`.env.example` is the template listing every variable.

## API

All responses are JSON; errors carry `{"error": "..."}`.

- `GET /api/devices` → `{"router_online": true, "devices": [{"mac", "name", "ip", "online", "policy_id"}]}`.
  An unreachable router is not an HTTP error here: the response comes back with
  `router_online: false` so the UI can show a banner instead of breaking.
- `GET /api/policies` → `{"policies": [{"id", "name"}]}`, including the synthetic
  `default` entry.
- `POST /api/devices/{mac}/policy` with `{"policy_id": "Policy0"}` →
  `{"ok": true, "mac": ..., "policy_id": ...}`. Use `policy_id: "default"` to
  reset the device to the router's default policy. Every write is followed by a
  configuration save in the same batch, so it survives a router reboot.

## Tests

```sh
go test ./...
go test -cover ./internal/...
```

All tests are deterministic and need neither network access nor a live router:
the RCI client is tested against an `httptest` mock (`internal/keenetic`), and
the HTTP API against a mock client implementation (`internal/api`). Fixtures —
sanitized copies of real router responses, with no real MACs or device names —
live in `internal/keenetic/testdata/`.

```sh
go vet ./...
golangci-lint run
```

## Layout

```
cmd/keenetic-policy-switcher/main.go  # config, server startup, routes
internal/keenetic/                    # RCI client: auth, reads, writes
internal/api/                         # HTTP API for the frontend (/api/*)
web/                                  # index.html + app.js + style.css, embedded
docs/api-notes.md                     # RCI paths and formats observed on a real router
```
