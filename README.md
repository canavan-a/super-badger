# super-badger

```
                                              🬵🬵🬱  🬊▒    🬭
                                  🬵🬭🬭🬵████████🬹🬝🬹█▒▒▒ 🬭  🬭
                              🬭🬹██████🬊🬂🬂🬂🬎🬎🬎🬎🬂🬀▒🬹█🬱▒🬹▒ 🬂    🬭
                         🬵🬹█🬝🬎🬂🬎🬂🬎▒░░▒🬭▒░░░░░░🬎░░▒🬹🬊█▓ ▒  ▒ 🬂
                    🬭 🬭🬹🬂🬎▒▒▒▒░░░▒🬭🬵██🬆🬀░░░░░░░░░▒░🬹▒🬹🬆▒ ▒🬹🬊▒🬱
                  🬵░░░▒░░░░░░░░▒🬊██🬹█🬆░░░░░░░░░░░░░░░░░ 🬊🬭🬹🬵▒🬆
                  🬊░░░░🬹🬹🬹🬹🬱🬵🬭🬭🬱🬵🬹🬭▒▒░░░░░░░░░░░🬎░░░🬆🬂 🬭 ▒▒
                    🬊░🬹🬭🬭🬂🬎🬂🬎🬎🬬███████🬻██████🬞█🬹█▒░░░🬆🬂    🬭🬭
                      🬊░░░░░░░░░░🬭░░🬊🬨███████████▓🬆🬂🬎░░ 🬊🬵▒🬱
                          🬂🬂🬎🬎🬭🬹▒▒🬭🬵🬵█████████████🬱░🬊░🬎░🬱  🬂
                                  🬊🬂🬂🬎🬂🬎█████🬂🬎🬂🬱🬵▒░ 🬂🬭  🬂 🬭🬹🬭
                                    🬵🬵▓▒▒▒▒▒🬀▒▒▒▒▒🬎▒ ░░░░🬹░

  🬞🬹🬝🬬█🬓                            🬞🬻██🬺🬏🬇🬎█🬹         🬖
 🬵🬬█  ▐▌       🬞                   🬞🬝🬀 🬁🬨█▌ ▐█🬲       🬁█🬹🬭
 █🬁🬬🬺🬭  🬞█🬓 🬷🬲 🬨█🬱🬵🬺  🬞🬋█🬏 🬻🬱🬵🬲    🬉▌    █▌ 🬭█🬂   🬵🬺🬏  🬁🬊██🬱   🬷🬹🬏  🬞🬋█🬏 🬻🬱🬵🬱
 🬁🬪🬱🬊🬬🬺🬱 🬨▌ 🬨🬕  █▌ 🬬🬺 █ 🬨🬺 ▐█🬁🬆     🬊🬃 🬃🬞🬝 🬎🬎█🬺🬏🬞🬜 🬨█🬀 ▐█ 🬉█🬓🬦🬕 🬊█🬀🬞█ 🬨▌ ▐█🬁🬆
 🬭 🬂🬪🬱🬨█ ▐▌ ▐▌  █▌ ▐█▐█🬃   ▐█          🬵🬔    🬁█▌▐▌ ▐█  ▐█  🬬🬄█▌ 🬦█ ▐█    ▐█
 █🬏  🬆🬷🬝 🬷🬲 🬷🬲 🬵█🬲🬏▐🬄 █🬲   ▐█🬏        🬂🬬█🬹🬏   🬝🬀▐█🬏▐█  🬷█🬭 🬝 🬨🬺 ▐█🬏🬁█🬱   ▐█
 🬎🬎🬎🬺🬎🬆  🬂🬎🬄🬁🬎🬀 🬨🬝🬎🬀  🬁🬬🬎🬀 🬁🬊🬆          🬊🬎█🬛🬋🬂   🬊🬎🬂🬬🬆  🬂🬎🬆   🬎🬄🬁█▌ 🬁🬬🬎  🬁🬎🬄
                ▐🬄                                           🬞🬏  █🬀
                🬁                                            🬁🬎🬋🬅🬀

                        ▸ press any key to burrow in _
```

*The `superbadger` terminal client's launch art, shown here in monochrome — the real thing is truecolor, animated (a soft glimmer across the wordmark and the badger's eye, a blinking cursor). The badger is drawn at 2×3 pixels per character (sextants) with fur texture; `tui/art/superbadger.ans` is the original and `go run ./tools/enhance` (from `tui/`) regenerates `tui/internal/ui/splash.ans` from it.*

A highly opinionated server management tool for local LLM inference — **not
a harness**. super-badger doesn't run an agent loop itself; it sits in front
of one or more `opencode serve` instances (which do) and gives a simplified
API for managing local-LLM agent work: `Station`s.

It also includes a React Native app (bare RN + a Vite-powered
react-native-web target, one codebase for both) for viewing and interacting
with those Stations — a chat UI over a Station's session, live status, and
Android background notifications — not a second harness either, just a
client for the one above.

A **Station** groups one opencode agent + one provider/model (a specific
local LLM endpoint, e.g. a GPU-bound `home-nixllm-a`) + at most one live
opencode session. Since the local models behind a provider/model have no
request concurrency, creating/resetting a Station on a given backend
automatically aborts any other Station's session already pointed at that
same backend — super-badger enforces single-session-per-backend so you never
have to think about it.

## Layout

- `server/` — the Go module.
  - `cmd/superbadger-server` — server entrypoint.
  - `config` — env-based configuration.
  - `database` — SQLite (via `gorm` + `glebarez/sqlite`, pure Go, no cgo)
    schema for `stations` and `station_metrics`.
  - `opencode` — thin typed client for the opencode HTTP API.
  - `station` — Station lifecycle + single-session enforcement.
  - `metrics` — pluggable optional GPU/perf metrics polling.
  - `api` — super-badger's own HTTP API.
- `app/` — the React Native client (bare RN, not Expo). `npm run web` serves
  a Vite dev build of the exact same UI in a browser; `npm run android`
  builds the native target. See `app/README.md`. It opens on an animated, themed
  logo splash and has a per-theme launcher icon (the S), both generated from the
  terminal art by `tui/tools/appart`.
- `tui/` — the `superbadger` terminal client (Go, bubbletea; its own module,
  talks to the server over REST + WebSocket only). Chat-focused: pick/swap
  stations (the station screen shows just each one's metrics), streaming
  chat with permission/question prompts, the top bar
  (status, token count, data point badges, action buttons), and the same
  settings as the app — no graphs. Styled after `superbadger.ans`: a navy
  terminal window with a launch splash (`--no-splash` or Settings to skip), your
  messages as cyan-railed panels and the model's as flat blue-railed text.
  Install with
  `nix profile install github:canavan-a/super-badger#superbadger` (or add
  `packages.<system>.default` to `environment.systemPackages`); like the
  server it is `go build`-ed from source on first run, with no `vendorHash`.
  In the dev shell: `run-tui`. Keys: `ctrl+s` stations, `←`/`→` (with an empty input) or `ctrl+n`/`ctrl+p`
  swap, `ctrl+k` actions, `ctrl+e` station settings, `ctrl+g` settings,
  `ctrl+o` tool output, `ctrl+q`/`ctrl+c` quit (quitting never cancels a running
  reply; `Esc` does nothing in the message box). Typed commands: `/stop` (cancel
  the running reply and drop anything queued behind it), `/help` (list these),
  `/compact` (summarize older messages to free context), `/show` (toggle
  thinking; `ctrl+r` too), `/settings`, `/stations`, `/station`, `/splash`
  (replay the title screen), `/quit`. Typing `/` shows a dim ghost of the first
  matching command; `Tab` completes it and pressing `Tab` again cycles the
  matches. Themes: burrow (default), dark, slate,
  ember, light, sepia — the title screen is recolored to match.
  Data: the TUI keeps only its own settings (server URL, token, theme, last
  station, …) in `config.json` under your user config dir
  (`~/.config/superbadger/` on Linux, `~/Library/Application Support/superbadger/`
  on macOS; `$XDG_CONFIG_HOME` is honored), mode 0600. Chats and stations live on
  the server. Updates never touch it: the Nix wrapper only rebuilds its compiled
  binary under `~/.cache/superbadger/`. Saves are atomic, keys from other versions
  are preserved, and an unreadable file is set aside as `config.json.corrupt`.
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

No auth yet.
