# super-badger

<p align="center">
  <img src="docs/superbadger.png" alt="Super Badger: a pixel-art badger above a blackletter wordmark" width="720">
</p>

<p align="center"><sub>The title screen, rendered from <code>tui/art/superbadger.ans</code> (in the terminal it is animated: a soft glimmer across the wordmark and the badger's eye).</sub></p>

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

## Terminal client

`superbadger` is a full-screen terminal chat client for a super-badger server
(Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea), in `tui/`). It
covers the chat page and its settings: pick a station, chat with streaming
replies, and see the top bar you configured. It has no graphs. It only talks to
the server's REST + WebSocket API, so it works against a server you already run.

### Install

```sh
nix profile install github:canavan-a/super-badger#superbadger
# or in a NixOS config:
#   environment.systemPackages = [ inputs.super-badger.packages.${system}.default ];
```

Like the server, it is built from source rather than by a `buildGoModule`
derivation (so there is no `vendorHash` to keep in sync): the first run
`go build`s it (network needed once, for Go modules) and caches the binary in
`~/.cache/superbadger/`. It rebuilds only when `tui/` changes. From a checkout,
use `run-tui` in the dev shell, or `cd tui && go run ./cmd/superbadger`.

### First run

It opens on the title screen (any key continues; `--no-splash`, or Settings →
"Launch art", turns it off). There is no default server: until one is set you get
a welcome page, and `g` (or `/`) opens Settings, where you enter the **Server
URL** and, if the server has auth tokens, the **Auth token**
(`badger token generate <label>` on the server). For a single run you can pass
`superbadger --server http://host:8080 --token …` instead; flags are never saved.

### Chatting

- Replies stream in. Your messages are cyan-railed panels and the model's are
  flat, blue-railed text, so it's always clear who said what. Tool calls, files
  and sub-tasks show inline (`Ctrl+O` toggles their output), and the model's
  thinking is collapsed until you ask for it (`/show`).
- Type while a reply is running and your message is queued behind it.
- Permission requests and questions from the agent appear as a boxed prompt above
  the message box (`o` once / `a` always / `r` reject; for questions, `↑/↓`,
  `Space`, `Enter`, `c` for a free-text answer, `Esc` to reject).
- The top bar shows the station (in its color), its status, the context size if
  you enabled the token count, and the data points you set to show on the top
  bar. A second row shows its directory and any enabled action buttons.
  After each step the transcript shows `step done · 72.6k context · 7.5k thinking`;
  the context figure is the same number as the top bar's, and thinking tokens
  are listed separately because they are billed but don't stay in the context.
- Compaction (`/compact`) is announced in the top bar and the transcript for as
  long as it takes.
- With **Other-station alerts** on (Settings), another station finishing, asking
  for permission or a question, or a data point crossing its threshold shows a
  one-line notice and rings the terminal bell. Native desktop notifications for
  the same events are covered below.

### Desktop notifications

superbadger can raise a native desktop notification when an agent finishes,
needs a permission, asks a question, or a data point crosses its threshold. It
uses the platform's own tool, so there is nothing extra to install on most
machines:

| Platform | Uses | Notes |
| --- | --- | --- |
| Linux | `notify-send` (libnotify) | needs a desktop session; the Nix package brings its own `notify-send` as a fallback |
| macOS | `osascript` | ships with the OS |
| Windows, other | — | not supported yet; you're told so instead of getting an error |

- **When it fires.** For any station that is *not* the one on screen, and for the
  station on screen when your terminal is not focused. It stays quiet about the
  station you're looking at in a focused terminal. Focus is only known if your
  terminal reports it (most do; in tmux add `set -g focus-events on`); if it
  can't tell, it notifies rather than risk missing something. Bursts of the same
  event within 3 seconds are shown once.
- **It never gets in the way when it can't work.** If `notify-send` (or a desktop
  session) isn't there, or a send fails, the TUI keeps running, says so **once**
  ("desktop notifications unavailable: notify-send was not found — install
  libnotify …") and stops trying until you change a setting. Nothing crashes and
  the in-terminal toasts keep working.
- **Settings** (`/settings`): **Desktop notifications** turns them on or off and
  shows which tool is used (or why none is available); **Send a test
  notification** sends one right now and reports the result, so you can check
  your setup. They are separate from **Other-station alerts**, which controls the
  in-terminal toast and bell; either can be on without the other.

### Keys

| Key | Does |
| --- | --- |
| `Enter` | send (`Alt+Enter` or `Ctrl+J` for a newline) |
| `Ctrl+S` | station list: pick, create (`n`) or delete (`d`) a station |
| `←` / `→` on an empty box, or `Ctrl+N` / `Ctrl+P` | previous / next station |
| `Ctrl+K` | actions menu: compact, abort, reset, delete |
| `Ctrl+E` | this station's settings |
| `Ctrl+G` | settings |
| `Ctrl+O` / `Ctrl+R` | show or hide tool output / thinking |
| `PgUp` `PgDn` `Ctrl+U` `Ctrl+D`, mouse wheel | scroll the transcript |
| `Home` / `End` | start / end of the line (on an empty box: top / bottom of the transcript) |
| `Ctrl+←/→`, `Alt+←/→`, `Alt+B/F` | jump by word |
| `Alt+C` `Alt+R` `Alt+D` | compact / reset / delete, when enabled as buttons for the station |
| `Ctrl+Q`, `Ctrl+C` | quit. This never cancels a running reply, and `Esc` does nothing in the message box. |

### Commands

Type these in the message box (`/help` lists them in the app). Typing `/` shows a
dim ghost of the first match; `Tab` completes it and pressing `Tab` again cycles
through the others.

| Command | Does |
| --- | --- |
| `/help` | list the commands |
| `/stop` | cancel the running reply, and drop any messages queued behind it |
| `/compact` | summarize older messages to free up context (not while a reply is running) |
| `/show` | show or hide the model's thinking (`Ctrl+R` too) |
| `/settings` | open settings |
| `/stations` | pick or create a station |
| `/station` | settings for this station |
| `/splash` | replay the title screen (handy for previewing a theme) |
| `/quit`, `/exit` | quit |

Anything else starting with `/` is sent to the model as ordinary text.

### Settings

`Ctrl+G` or `/settings`. Everything the app's Settings screen has, except graphs:

- **Connection:** server URL, auth token, and a connection test.
- **Appearance:** theme (`burrow` (default), `dark`, `slate`, `ember`, `light`,
  `sepia`). Each theme paints its own background, and the title screen is
  recolored to match.
- **Notifications:** other-station alerts (in-terminal), desktop notifications, and
  a test button (see above). **Launch art** switch.
- **Metric sources** (add, edit, enable/disable, delete) and **Mullvad VPN**
  (status, connect/disconnect, location, LAN access).
- **Station settings** (`Ctrl+E`): name, alias, color, which buttons and the token
  count show on the top bar, and each data point's label, decimals, top-bar
  visibility, threshold alert, and order (`Alt+↑/↓`).

### Where its data lives

Only its own settings (server URL, token, theme, last station, …), in
`config.json` under your user config dir (`~/.config/superbadger/` on Linux,
`~/Library/Application Support/superbadger/` on macOS; `$XDG_CONFIG_HOME` is
honored), readable only by you. Chats and stations live on the server. Updates
never touch that file: the Nix wrapper only rebuilds its compiled binary under
`~/.cache/superbadger/`. Saves are atomic, settings written by another version are
kept, and an unreadable file is set aside as `config.json.corrupt`.

### Development

```sh
cd tui
go test ./...                                    # tests never touch your real config
go run ./tools/enhance                           # rebuild the title art from art/superbadger.ans
go run ./tools/appart                            # logo data + launcher icons for the phone app
go run ./tools/appart -image ../docs/superbadger.png   # the picture at the top of this file
```

The title art (`tui/art/superbadger.ans`) is the single source for the terminal
splash, the phone app's splash and launcher icons, and the image above.

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
- `tui/` — the `superbadger` terminal client (Go, its own module; talks to the
  server over REST + WebSocket only). See [Terminal client](#terminal-client).
  `tui/art` holds the title art the app's splash and icons are generated from.
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
