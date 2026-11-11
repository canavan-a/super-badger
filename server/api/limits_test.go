package api

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIsLocal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for remote, want := range map[string]bool{
		"127.0.0.1:5000": true,
		"[::1]:5000":     true,
		"203.0.113.9:80": false, // TEST-NET-3, never a local interface
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/internal/limits", nil)
		c.Request.RemoteAddr = remote
		if got := isLocal(c); got != want {
			t.Errorf("isLocal(%s) = %v, want %v", remote, got, want)
		}
	}
}

func TestIsHostAddr(t *testing.T) {
	addrs := func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("100.64.1.2"), Mask: net.CIDRMask(32, 32)},
			&net.IPNet{IP: net.ParseIP("192.168.1.10"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}
	// The host's own Tailscale / LAN address: the plugin connecting to a
	// server bound to that IP.
	if !isHostAddr(net.ParseIP("100.64.1.2"), addrs) {
		t.Error("own Tailscale address not local")
	}
	if !isHostAddr(net.ParseIP("192.168.1.10"), addrs) {
		t.Error("own LAN address not local")
	}
	// Another machine on the same subnet is not this host.
	if isHostAddr(net.ParseIP("192.168.1.11"), addrs) {
		t.Error("another LAN machine counted as local")
	}
	if isHostAddr(net.ParseIP("127.0.0.1"), func() ([]net.Addr, error) { return nil, errors.New("boom") }) {
		t.Error("an interface lookup error must not allow access")
	}
}
