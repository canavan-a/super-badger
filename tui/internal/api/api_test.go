package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthHeaderAndErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/stations/9" {
			w.WriteHeader(409)
			w.Write([]byte(`{"error":"name taken"}`))
			return
		}
		w.Write([]byte(`[{"id":1,"name":"a","top_bar_actions":["tokens"],"reachable":true}]`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/", "secret")
	l, err := c.ListStations()
	if err != nil || len(l) != 1 || !l[0].HasAction("tokens") || l[0].HasAction("reset") {
		t.Fatalf("list = %+v, %v", l, err)
	}
	if _, err := c.GetStation(9); err == nil || !strings.Contains(err.Error(), "name taken") {
		t.Fatalf("want server error text, got %v", err)
	}
	if _, err := New(srv.URL, "wrong").ListStations(); err == nil {
		t.Fatal("expected auth failure")
	}
}

func TestStationUpdateOmitsUnsetFields(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()
	empty := ""
	acts := []string{}
	c := New(srv.URL, "")
	// Alias "" must be sent (clears it); an empty actions list must be sent too.
	if _, err := c.UpdateStation(1, StationUpdate{Alias: &empty, TopBarActions: &acts}); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["name"]; ok {
		t.Fatalf("name should be omitted: %v", body)
	}
	if v, ok := body["alias"]; !ok || v != "" {
		t.Fatalf("alias not sent: %v", body)
	}
	if v, ok := body["top_bar_actions"].([]any); !ok || len(v) != 0 {
		t.Fatalf("actions not sent as []: %v", body)
	}
}

func TestWSURLAndFormatting(t *testing.T) {
	if got := New("https://h:1", "a b").WSURL("/x"); got != "wss://h:1/x?token=a+b" {
		t.Fatal(got)
	}
	if got := New("http://h", "").WSURL("/x"); got != "ws://h/x" {
		t.Fatal(got)
	}
	if FormatTokens(999) != "999" || FormatTokens(1500) != "1.5k" {
		t.Fatal("FormatTokens")
	}
	if FormatValue(3.14159, 2) != "3.14" || FormatValue(3, 9) != "3.000000" {
		t.Fatal("FormatValue")
	}
}
