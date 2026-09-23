# NixOS module for super-badger: opencode serve, managed as a dependency, plus
# the superbadger Go server in front of it.
#
# superbadger is built by an activation script, not a buildGoModule
# derivation — a vendorHash kept in sync by hand on every go.sum change is the
# same maintenance cost as npm's npmDepsHash, for a binary with no
# reproducibility requirement that would justify carrying it (same tradeoff
# horus-33 makes for horus-server). It rebuilds from ${self}/server on every
# activation instead; goCache persists the module/build cache across rebuilds
# so this stays fast after the first run. The mobile app's web build
# (services.superbadger.web) follows the exact same tradeoff via npmCache
# instead of an npmDepsHash.
{ self }:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.superbadger;
  serverBin = "/var/lib/superbadger/bin/superbadger";
  badgerBin = "/var/lib/superbadger/bin/badger";
  goCache = "/var/cache/superbadger-go";
  npmCache = "/var/cache/superbadger-npm";
  webDir = "/var/lib/superbadger/web";

  # System-wide `badger` command (e.g. `badger token generate`) — a thin
  # wrapper so an admin doesn't have to know/export SUPERBADGER_DB_PATH by
  # hand to point it at the same database the running service uses.
  badgerWrapper = pkgs.writeShellScriptBin "badger" ''
    export SUPERBADGER_DB_PATH="${cfg.dbPath}"
    exec ${badgerBin} "$@"
  '';
in
{
  options.services.superbadger = {
    enable = lib.mkEnableOption "superbadger opencode management server";

    listenAddr = lib.mkOption {
      type = lib.types.str;
      default = ":8080";
      description = "Address for superbadger's own HTTP API to listen on.";
    };

    dbPath = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/superbadger/superbadger.db";
      description = "Path to the SQLite database file.";
    };

    opencode = {
      port = lib.mkOption {
        type = lib.types.port;
        default = 4096;
        description = "Port for the managed opencode serve instance.";
      };

      hostname = lib.mkOption {
        type = lib.types.str;
        default = "127.0.0.1";
        description = "Hostname for the managed opencode serve instance to bind to.";
      };

      package = lib.mkOption {
        type = lib.types.package;
        default = pkgs.opencode or (throw "pkgs.opencode not found; set services.superbadger.opencode.package");
        description = "opencode package providing the `opencode` binary.";
      };
    };

    metrics = {
      url = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        description = "Optional URL of an external metrics ('superbadger spec') endpoint to poll for GPU/perf stats.";
      };

      interval = lib.mkOption {
        type = lib.types.str;
        default = "30s";
        description = "Poll interval for the metrics endpoint, as a Go duration string.";
      };
    };

    web = {
      enable = lib.mkEnableOption "serving the mobile app's built web client from superbadger, on its own port";

      listenAddr = lib.mkOption {
        type = lib.types.str;
        default = ":8081";
        description = ''
          Address for the static web client to listen on — separate from
          listenAddr (the API). No auth or CORS is applied here (see
          server/static); exposing this externally (reverse proxy, tunnel,
          etc.) is left up to you.
        '';
      };
    };

    mullvad = {
      enable = lib.mkEnableOption "Mullvad VPN, controllable through superbadger's API";

      allowLan = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          Whether local network traffic (e.g. 192.168.0.0/16, 10.0.0.0/8,
          172.16.0.0/12) bypasses the Mullvad tunnel instead of being
          blocked/routed through it. This is Mullvad's own "local network
          sharing" setting (`mullvad lan set allow`), applied once at
          activation; it can still be flipped at runtime via the
          /mullvad/lan API endpoint.
        '';
      };
    };
  };

  config = lib.mkIf cfg.enable {
    services.mullvad-vpn = lib.mkIf cfg.mullvad.enable {
      enable = true;
    };

    # mullvad-daemon's control socket (/run/mullvad-vpn) is world read/write,
    # so the unprivileged superbadger user can drive the `mullvad` CLI
    # without extra group wiring. Apply the LAN passthrough setting once at
    # activation, after the daemon unit exists; the API can still change it
    # at runtime.
    systemd.services.superbadger-mullvad-lan = lib.mkIf cfg.mullvad.enable {
      description = "Apply superbadger's configured Mullvad LAN sharing setting";
      after = [ "mullvad-daemon.service" ];
      wants = [ "mullvad-daemon.service" ];
      wantedBy = [ "multi-user.target" ];
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        ExecStart = "${pkgs.mullvad-vpn}/bin/mullvad lan set ${if cfg.mullvad.allowLan then "allow" else "block"}";
        # after/wants above only order this against mullvad-daemon.service's
        # own start, not against its RPC socket actually being ready —
        # `mullvad lan set` can still lose that race and fail with "transport
        # error ... No such file or directory" on a socket that hasn't been
        # created yet. Retrying instead of failing outright rides that out.
        Restart = "on-failure";
        RestartSec = "2s";
      };
    };

    systemd.tmpfiles.rules = [
      "d /var/lib/superbadger 0750 superbadger superbadger -"
      "d /var/lib/superbadger/bin 0755 root root -"
      "d ${goCache} 0755 root root -"
    ] ++ lib.optionals cfg.web.enable [
      "d ${npmCache} 0755 root root -"
    ];

    users.users.superbadger = {
      isSystemUser = true;
      group = "superbadger";
    };
    users.groups.superbadger = { };

    system.activationScripts.superbadgerBuild = {
      deps = [ ];
      text = ''
        export PATH=${pkgs.go}/bin:${pkgs.bash}/bin:${pkgs.nodejs_22}/bin:$PATH
        export HOME=${goCache}
        export GOCACHE=${goCache}/build
        export GOPATH=${goCache}/path
        # glebarez/sqlite (modernc.org/sqlite under it) is pure Go; force
        # CGO off so the build doesn't need a C toolchain on PATH.
        export CGO_ENABLED=0
        mkdir -p /var/lib/superbadger/bin ${goCache}
        cd ${self}/server
        ${pkgs.go}/bin/go build -o ${serverBin}.new ./cmd/superbadger
        mv -f ${serverBin}.new ${serverBin}
        ${pkgs.go}/bin/go build -o ${badgerBin}.new ./cmd/badger
        mv -f ${badgerBin}.new ${badgerBin}
      '' + lib.optionalString cfg.web.enable ''
        # Same rebuild-from-source-every-activation tradeoff as the Go build
        # above, applied to the mobile app's web target (npm ci + vite
        # build) instead of a buildNpmPackage derivation that would need an
        # npmDepsHash kept in sync by hand.
        #
        # Unlike the Go build above, this can't run straight out of
        # ${self}/app: that's a path into the Nix store (self is a flake
        # input), which is read-only, and `npm ci` needs to write
        # node_modules directly into the project directory it's run from —
        # go build has no such requirement, it only reads its source dir and
        # writes elsewhere (GOCACHE/output), which is why that half of this
        # script could get away with running in-place. Copying the app
        # source into a writable scratch dir first is what actually lets npm
        # ci succeed.
        export npm_config_cache=${npmCache}
        appBuild=/var/lib/superbadger/app-build
        rm -rf "$appBuild"
        mkdir -p "$appBuild"
        cp -r ${self}/app/. "$appBuild"/
        cd "$appBuild"
        ${pkgs.nodejs_22}/bin/npm ci
        ${pkgs.nodejs_22}/bin/npm run build:web
        rm -rf ${webDir}.new
        cp -r dist ${webDir}.new
        chown -R superbadger:superbadger ${webDir}.new
        rm -rf ${webDir}
        mv -f ${webDir}.new ${webDir}
      '' + ''
        ${pkgs.systemd}/bin/systemctl try-restart superbadger.service || true
      '';
    };

    environment.systemPackages = [ badgerWrapper ];

    systemd.services.opencode-serve = {
      description = "opencode headless server (managed by superbadger)";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" ];
      serviceConfig = {
        ExecStart = "${cfg.opencode.package}/bin/opencode serve --port ${toString cfg.opencode.port} --hostname ${cfg.opencode.hostname}";
        Restart = "always";
        RestartSec = 2;
      };
    };

    systemd.services.superbadger = {
      description = "superbadger opencode management server";
      after = [ "network.target" "opencode-serve.service" ];
      wants = [ "opencode-serve.service" ];
      wantedBy = [ "multi-user.target" ];
      environment = {
        SUPERBADGER_LISTEN_ADDR = cfg.listenAddr;
        SUPERBADGER_DB_PATH = cfg.dbPath;
        SUPERBADGER_OPENCODE_URL = "http://${cfg.opencode.hostname}:${toString cfg.opencode.port}";
      } // lib.optionalAttrs (cfg.metrics.url != null) {
        SUPERBADGER_METRICS_URL = cfg.metrics.url;
        SUPERBADGER_METRICS_INTERVAL = cfg.metrics.interval;
      } // lib.optionalAttrs cfg.mullvad.enable {
        SUPERBADGER_MULLVAD_BIN = "${pkgs.mullvad-vpn}/bin/mullvad";
      } // lib.optionalAttrs cfg.web.enable {
        SUPERBADGER_STATIC_DIR = webDir;
        SUPERBADGER_STATIC_LISTEN_ADDR = cfg.web.listenAddr;
      };
      serviceConfig = {
        ExecStart = serverBin;
        Restart = "always";
        RestartSec = 2;
        User = "superbadger";
        Group = "superbadger";
      };
    };
  };
}
