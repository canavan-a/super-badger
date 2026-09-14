// Package mullvad drives the local `mullvad` CLI (from the mullvad-vpn
// daemon/client, managed by the NixOS module) to expose connect/disconnect,
// account configuration, and relay location selection over super-badger's
// own HTTP API. It shells out rather than talking to the daemon's gRPC
// socket directly, since the CLI is the stable, documented surface and this
// is a low-throughput control path (a handful of operator actions, not a
// hot loop).
package mullvad

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Client struct {
	bin string
}

// NewClient returns a Client that invokes the given `mullvad` binary (or
// "mullvad" to resolve it from PATH).
func NewClient(bin string) *Client {
	if bin == "" {
		bin = "mullvad"
	}
	return &Client{bin: bin}
}

func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return "", fmt.Errorf("mullvad %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return out.String(), nil
}

// Status returns the raw output of `mullvad status`.
func (c *Client) Status(ctx context.Context) (string, error) {
	return c.run(ctx, "status")
}

// Connect brings the tunnel up.
func (c *Client) Connect(ctx context.Context) (string, error) {
	return c.run(ctx, "connect")
}

// Disconnect tears the tunnel down.
func (c *Client) Disconnect(ctx context.Context) (string, error) {
	return c.run(ctx, "disconnect")
}

// Login sets the account token used to authenticate with Mullvad.
func (c *Client) Login(ctx context.Context, accountToken string) (string, error) {
	if accountToken == "" {
		return "", fmt.Errorf("account token required")
	}
	return c.run(ctx, "account", "login", accountToken)
}

// Logout clears the configured account token.
func (c *Client) Logout(ctx context.Context) (string, error) {
	return c.run(ctx, "account", "logout")
}

// SetLocation selects the relay location to connect through. country is
// required; city and hostname are optional, matching
// `mullvad relay set location <country> [city] [hostname]`.
func (c *Client) SetLocation(ctx context.Context, country, city, hostname string) (string, error) {
	if country == "" {
		return "", fmt.Errorf("country required")
	}
	args := []string{"relay", "set", "location", country}
	if city != "" {
		args = append(args, city)
	}
	if hostname != "" {
		args = append(args, hostname)
	}
	return c.run(ctx, args...)
}

// ListRelays returns the raw output of `mullvad relay list`, the set of
// valid country/city/hostname values for SetLocation.
func (c *Client) ListRelays(ctx context.Context) (string, error) {
	return c.run(ctx, "relay", "list")
}

// SetLAN toggles local network sharing: when allowed, traffic to local
// network ranges (e.g. 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12) bypasses
// the tunnel instead of being blocked/routed through it. This is Mullvad's
// built-in passthrough for LAN traffic — there is no separate proxy setting
// needed for it.
func (c *Client) SetLAN(ctx context.Context, allow bool) (string, error) {
	v := "block"
	if allow {
		v = "allow"
	}
	return c.run(ctx, "lan", "set", v)
}
