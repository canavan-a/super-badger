# NixOS module for super-badger: opencode serve, managed as a dependency, plus
# the superbadger Go server in front of it.
#
# superbadger is built by an activation script, not a buildGoModule
# derivation — a vendorHash kept in sync by hand on every go.sum change is the
# same maintenance cost as npm's npmDepsHash, for a binary with no
# reproducibility requirement that would justify carrying it (same tradeoff
# horus-33 makes for horus-server). It rebuilds from ${self}/server on every
# activation instead; goCache persists the module/build cache across rebuilds
# so this stays fast after the first run.
{ self }:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.superbadger;
  serverBin = "/var/lib/superbadger/bin/superbadger";
  goCache = "/var/cache/superbadger-go";
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
      };
    };

    systemd.tmpfiles.rules = [
      "d /var/lib/superbadger 0750 superbadger superbadger -"
      "d /var/lib/superbadger/bin 0755 root root -"
      "d ${goCache} 0755 root root -"
    ];

    users.users.superbadger = {
      isSystemUser = true;
      group = "superbadger";
    };
    users.groups.superbadger = { };

    system.activationScripts.superbadgerBuild = {
      deps = [ ];
      text = ''
        export PATH=${pkgs.go}/bin:${pkgs.bash}/bin:$PATH
        export HOME=${goCache}
        export GOCACHE=${goCache}/build
        export GOPATH=${goCache}/path
        mkdir -p /var/lib/superbadger/bin ${goCache}
        cd ${self}/server
        ${pkgs.go}/bin/go build -o ${serverBin}.new ./cmd/superbadger
        mv -f ${serverBin}.new ${serverBin}
        ${pkgs.systemd}/bin/systemctl try-restart superbadger.service || true
      '';
    };

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
