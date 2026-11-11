# Super Galleon

<p align="center">
  <img src="docs/supergalleon.png" alt="Super Galleon: a pixel-art galleon above a blackletter wordmark" width="720">
</p>

A self-hosted control plane for coding agents running on **your own models**.

Super Galleon is **not a harness**. It doesn't run an agent loop; it sits in front
of `opencode serve` (opencode 2.2 or later) (which does) and adds what running agents against local
hardware needs and hosted-API tools don't bother with. You then use those agents
from a terminal client, a phone app, or a browser.

## Why it exists

Most agent harnesses assume a hosted API: the model is always up, the context
window is huge, and you watch the agent at your desk. Local inference breaks all
three assumptions. Super Galleon is built around that:

- **Is the model actually up?** Every station live-pings its model's endpoint
  (the `baseURL` from your opencode provider config: llama.cpp, vLLM, Ollama,
  …) whenever it's listed. If you stop the GPU box, the station shows
  unreachable right away, not after a prompt hangs. Hosted providers are skipped.
- **Your hardware's metrics next to the chat.** Point Super Galleon at any HTTP
  endpoint returning `{"<station>": {"gpu_temp": 71, "tok_s": 38.2, …}}` and
  those numbers show up on the station's top bar, with history and threshold
  alerts ("VRAM over 95%", "GPU over 85°C"). Bring your own exporter; no
  agent-side plugin needed.
- **Context that fits.** Small local context windows fill fast, and opencode
  can only compact in time if it knows the real window. Super Galleon's opencode
  plugin (`opencode-plugin/`) reads it from each llama.cpp or Strata engine
  (`/props`), follows it live when the engine or its context changes, and
  lets you override it per model from the app or terminal. Each station shows
  its context against that window after every step, and `/compact` is one
  keystroke away.
- **Compaction that can't get stuck.** If a compaction's summary fails, the
  plugin redoes it at once with a simpler, tool-free summary; if that fails
  too, the station moves to a fresh session seeded with the last summary and
  the newest turns. With auto mode on, the interrupted turn then continues by
  itself.
- **Walk away from long runs.** Local models are slow, so runs are long. When a
  station finishes, asks for permission, asks a question, or crosses a metric
  threshold, you get a notification (Android background push, desktop, or
  terminal bell), and you can answer the permission prompt from your phone.
- **Stations, not sessions.** A *Station* is one model + one working directory +
  one persistent opencode session, with a name and a color. Keep one per
  project or per model, switch between them, and reset them when you need to.
- **Runs wherever opencode does.** It doesn't need to be on the inference box:
  any machine that can reach your model endpoints works. It's a single Go
  binary with SQLite (no cgo) and a NixOS module that also runs opencode. It
  has token auth, can serve the web client itself, and can optionally drive
  Mullvad so the host can be reached remotely while LAN traffic to your models
  stays local.

## Clients

All three talk only to the server's REST + WebSocket API.

- **`superbadger` terminal client** (`tui/`, Go + Bubble Tea): streaming
  chat, inline tool calls, and permission/question prompts. It also covers
  station and metric settings and desktop notifications. `Ctrl+G` opens
  settings and `/help` lists commands.
- **Phone / web app** (`app/`, bare React Native + a Vite react-native-web
  build, one codebase): the same chat, plus metric graphs and Android
  background notifications.

```sh
nix profile install github:canavan-a/super-badger#superbadger
superbadger --server http://badger-host:8080 --token <token>   # or set both in Settings
```

## Running it

> Super Galleon was called super-badger. Commands (`superbadger`, `badger`), the
> NixOS module (`services.superbadger`), `SUPERBADGER_*` environment variables,
> data paths and the Android package keep that name, so existing installs and
> phones upgrade in place.

On NixOS:

```nix
services.superbadger = {
  enable = true;
  opencode.port = 4096;   # the managed `opencode serve`
  web.enable = true;      # serve the web client on :8081
  mullvad.enable = false;
};
```

Then mint a client token on that machine with `badger token generate <label>`.
Auth kicks in once the first token exists.

Models are configured in **opencode's** provider config, not in Super Galleon.
Stations pick from whatever `GET /providers` reports.

For development, `nix develop` then `dev-up` / `dev-down` (opencode + server with
hot reload, logs in `.data/logs`); `dev-help` lists the rest. Server config is
environment variables: `SUPERBADGER_LISTEN_ADDR` (`:8080`),
`SUPERBADGER_DB_PATH`, `SUPERBADGER_OPENCODE_URL` (`http://127.0.0.1:4096`),
`SUPERBADGER_OPENCODE_PASSWORD` (or `SUPERBADGER_OPENCODE_PASSWORD_FILE`; opencode 2
always requires it, and the NixOS module generates one), `SUPERBADGER_STATIC_DIR`.

## Layout

- `server/`: Go API server (`cmd/superbadger-server`) and the `badger` admin
  CLI (`cmd/badger`)
- `tui/`: terminal client (its own Go module)
- `app/`: React Native + web client
- `opencode-plugin/`: the opencode plugin the module loads (context limits,
  compaction recovery); `bun test` runs its tests
- `flake.nix`, `module.nix`: dev shell, packages, NixOS module
