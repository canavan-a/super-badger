# super-badger
highly opinionated server manager

A Go server that sits in front of one or more `opencode serve` instances and
gives a simplified API for managing local-LLM agent work: `Station`s.

A **Station** groups one opencode agent + one provider/model (a specific
local LLM endpoint, e.g. a GPU-bound `home-nixllm-a`) + at most one live
opencode session. Since the local models behind a provider/model have no
request concurrency, creating/resetting a Station on a given backend
automatically aborts any other Station's session already pointed at that
same backend — super-badger enforces single-session-per-backend so you never
have to think about it.

## Layout

- `server/` — the Go module.
  - `cmd/superbadger` — entrypoint.
  - `config` — env-based configuration.
  - `database` — SQLite (via `gorm` + `glebarez/sqlite`, pure Go, no cgo)
    schema for `stations` and `station_metrics`.
  - `opencode` — thin typed client for the opencode HTTP API.
  - `station` — Station lifecycle + single-session enforcement.
  - `metrics` — pluggable optional GPU/perf metrics polling.
  - `api` — super-badger's own HTTP API.
- `flake.nix` / `module.nix` — Nix dev shell and NixOS module. superbadger is
  built from source by an activation script rather than a `buildGoModule`
  derivation, to avoid hand-maintaining a `vendorHash`.

## Running locally

`nix develop` at the repo root gives a dev shell with everything wired up
(assumes `opencode` is already installed/on `PATH` — see `flake.nix`):

```
nix develop
dev-up      # starts opencode + superbadger together, headless, logs to .data/logs
dev-down    # stops both
```

Or run each piece in its own terminal:

```
run-opencode      # foreground: opencode serve on :4096
run-superbadger   # foreground: superbadger with hot reload (air), pointed at :4096
db-reset          # delete the local sqlite db
dev-help          # re-print this list
```

Config is via environment variables (all optional):

- `SUPERBADGER_LISTEN_ADDR` (default `:8080`)
- `SUPERBADGER_DB_PATH` (default `superbadger.db`)
- `SUPERBADGER_OPENCODE_URL` (default `http://127.0.0.1:4096`)
- `SUPERBADGER_METRICS_URL` (optional external metrics poll endpoint)
- `SUPERBADGER_METRICS_INTERVAL` (default `30s`)

## API

- `POST /stations` `{name, provider_id, model_id, agent}` — create a Station,
  provisioning an opencode session.
- `GET /stations`, `GET /stations/:id` — list/get.
- `POST /stations/:id/prompt` `{text}` — send a prompt to the Station's
  session.
- `POST /stations/:id/reset` — re-provision the Station's session.
- `DELETE /stations/:id` — tear down the session and remove the Station.
- `GET /providers`, `GET /models` — passthrough to opencode's own
  provider/model config, for populating a create-Station form.

No auth yet — planned for when the mobile client work starts.
