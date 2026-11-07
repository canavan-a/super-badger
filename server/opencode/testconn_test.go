package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func modelsServer(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
	}))
}

func TestModelEndpointAlive(t *testing.T) {
	cases := []struct {
		status int
		alive  bool
	}{
		{http.StatusOK, true},
		{http.StatusUnauthorized, true}, // up, wants a key the check doesn't have
		{http.StatusForbidden, true},
		{http.StatusNotFound, false},           // wrong base URL
		{http.StatusServiceUnavailable, false}, // still loading the model
		{http.StatusInternalServerError, false},
	}
	for _, c := range cases {
		srv := modelsServer(c.status)
		if got := ModelEndpointAlive(context.Background(), srv.URL+"/v1", ""); got != c.alive {
			t.Errorf("status %d: alive = %v, want %v", c.status, got, c.alive)
		}
		srv.Close()
	}

	srv := modelsServer(http.StatusOK)
	srv.Close()
	if ModelEndpointAlive(context.Background(), srv.URL+"/v1", "") {
		t.Error("closed server: alive = true, want false")
	}
}

func TestModelConnectionStaysStrictOnAuth(t *testing.T) {
	srv := modelsServer(http.StatusUnauthorized)
	defer srv.Close()
	if err := TestModelConnection(context.Background(), srv.URL+"/v1", "wrong"); err == nil {
		t.Error("401: TestModelConnection = nil, want an error (a rejected key must fail validation)")
	}
}
