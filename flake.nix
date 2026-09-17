{
  description = "super-badger — opencode management server, dev shell + NixOS module";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-26.05";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    let
      perSystem = flake-utils.lib.eachDefaultSystem (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};

          # $PWD is resolved at runtime, so these scripts must be run from the
          # repo root (i.e. always `cd` into super-badger/ before `nix develop`).
          repoRoot = "$PWD";
          dataDir = "${repoRoot}/.data";
          dbPath = "${dataDir}/superbadger.db";

          opencodePort = "4096";
          superbadgerAddr = ":8080";
          # Local-only loopback dev password — not a secret worth generating
          # per-machine, just enough to keep opencode out of its unsecured
          # mode. superbadger authenticates to opencode with this via HTTP
          # Basic auth (SUPERBADGER_OPENCODE_PASSWORD, see server/config).
          opencodePassword = "password";

          # opencode (1.18.30 as installed here) crashes at startup trying to
          # use a legacy `opencode-stable.db` compat path it detects under
          # ~/.local/share/opencode — confirmed live; this env var (which
          # opencode's own error message names) disables that check.
          opencodeEnv = ''
            export OPENCODE_SERVER_PASSWORD="${opencodePassword}"
            export NIXPKGS_OPENCODE_DISABLE_LEGACY_DB_WORKAROUND=1
          '';

          db-reset = pkgs.writeShellScriptBin "db-reset" ''
            set -e
            rm -f "${dbPath}"
            echo "removed ${dbPath} (a fresh one is created on next superbadger start)"
          '';

          run-opencode = pkgs.writeShellScriptBin "run-opencode" ''
            set -e
            ${opencodeEnv}
            exec opencode serve --port ${opencodePort} --hostname 127.0.0.1
          '';

          # foreground, hot-reload via air (see server/.air.toml)
          run-superbadger = pkgs.writeShellScriptBin "run-superbadger" ''
            set -e
            mkdir -p "${dataDir}"
            cd "${repoRoot}/server"
            export SUPERBADGER_LISTEN_ADDR="${superbadgerAddr}"
            export SUPERBADGER_DB_PATH="${dbPath}"
            export SUPERBADGER_OPENCODE_URL="http://127.0.0.1:${opencodePort}"
            export SUPERBADGER_OPENCODE_PASSWORD="${opencodePassword}"
            exec ${pkgs.air}/bin/air
          '';

          dev-up = pkgs.writeShellScriptBin "dev-up" ''
            set -e
            # Capture as real bash variables *before* any `cd` below — the
            # superbadger subshell changes directory, and a Nix-interpolated
            # path containing a literal "$PWD" would otherwise re-expand
            # against the *new* cwd every time it's referenced afterwards.
            data_dir="$PWD/.data"
            db_path="$data_dir/superbadger.db"
            mkdir -p "$data_dir/logs"
            echo "starting opencode + superbadger in background (logs in $data_dir/logs)"

            (${opencodeEnv}
             opencode serve --port ${opencodePort} --hostname 127.0.0.1 > "$data_dir/logs/opencode.log" 2>&1 &)
            sleep 1
            (cd "${repoRoot}/server" && \
              SUPERBADGER_LISTEN_ADDR="${superbadgerAddr}" \
              SUPERBADGER_DB_PATH="$db_path" \
              SUPERBADGER_OPENCODE_URL="http://127.0.0.1:${opencodePort}" \
              SUPERBADGER_OPENCODE_PASSWORD="${opencodePassword}" \
              ${pkgs.go}/bin/go run ./cmd/superbadger > "$data_dir/logs/superbadger.log" 2>&1 &)

            echo "opencode:    http://127.0.0.1:${opencodePort} (log: $data_dir/logs/opencode.log)"
            echo "superbadger: http://127.0.0.1${superbadgerAddr} (log: $data_dir/logs/superbadger.log)"
            echo "run 'dev-down' to stop everything"
          '';

          # e.g. `badger token generate aidan` — same DB the dev superbadger
          # instance above uses (SUPERBADGER_DB_PATH), so a token minted here
          # is immediately valid against it, no restart needed. Named to
          # match cmd/badger exactly (same name prod's systemPackages wrapper
          # uses — see module.nix) so the command is identical in both
          # places.
          badger = pkgs.writeShellScriptBin "badger" ''
            set -e
            mkdir -p "${dataDir}"
            cd "${repoRoot}/server"
            export SUPERBADGER_DB_PATH="${dbPath}"
            exec ${pkgs.go}/bin/go run ./cmd/badger "$@"
          '';

          dev-down = pkgs.writeShellScriptBin "dev-down" ''
            set -e
            pkill -f "opencode serve --port ${opencodePort}" 2>/dev/null || true
            pkill -f "cmd/superbadger" 2>/dev/null || true
            pkill -f "go-build.*superbadger" 2>/dev/null || true
            pkill -f "tmp/superbadger" 2>/dev/null || true
            echo "stopped opencode + superbadger (best-effort)"
          '';

          printHelp = ''
            echo "super-badger dev shell"
            echo "  run-opencode     # foreground: opencode serve on :${opencodePort}"
            echo "  run-superbadger  # foreground: superbadger with hot reload (air), pointed at opencode on :${opencodePort}"
            echo "  dev-up           # start opencode + superbadger together (headless, logs to file)"
            echo "  dev-down         # stop everything started by dev-up"
            echo "  db-reset         # delete the local sqlite db (fresh one created on next start)"
            echo "  badger           # manage API auth tokens, e.g. 'badger token generate <label>'"
            echo "  run-app-web      # foreground: mobile app web target via Vite (hot reload) on :5173"
            echo "  run-app-android  # foreground: mobile app on a connected Android device/emulator (needs system Android SDK)"
            echo "  dev-help         # re-print this list"
            echo "  (run-opencode / run-superbadger / run-app-web / run-app-android each attach to your terminal — run each in its own terminal, inside 'nix develop')"
            echo ""
            echo "superbadger api: http://127.0.0.1:8080   sqlite db: ${dbPath}"
          '';

          run-app-web = pkgs.writeShellScriptBin "run-app-web" ''
            set -e
            cd "${repoRoot}/app"
            exec ${pkgs.nodejs_22}/bin/npm run web
          '';

          # Requires the system-wide Android SDK/emulator already on this
          # machine's PATH (see module.nix's top comment / horus-33/mobile) —
          # nothing Android-related is added to this flake on purpose.
          run-app-android = pkgs.writeShellScriptBin "run-app-android" ''
            set -e
            cd "${repoRoot}/app"
            exec ${pkgs.nodejs_22}/bin/npm run android
          '';

          dev-help = pkgs.writeShellScriptBin "dev-help" printHelp;
        in
        {
          devShells.default = pkgs.mkShell {
            buildInputs = [
              pkgs.go
              pkgs.gopls
              pkgs.air
              pkgs.sqlite
              pkgs.nodejs_22
              pkgs.watchman
              db-reset
              badger
              run-opencode
              run-superbadger
              run-app-web
              run-app-android
              dev-up
              dev-down
              dev-help
            ];

            shellHook = printHelp;
          };
        }
      );

      # No buildGoModule package output on purpose: a vendorHash kept in sync
      # by hand on every go.sum change is the same maintenance cost as npm's
      # npmDepsHash, for a binary with no reproducibility requirement that
      # would justify carrying it (same tradeoff as horus-33's horus-server).
      # The NixOS module below builds superbadger from source in an
      # activation script instead.
      nixosModules.default = import ./module.nix { inherit self; };
    in
    perSystem // { inherit nixosModules; };
}
