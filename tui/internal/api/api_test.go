package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestCompactOutlastsTheNormalRequestTimeout(t *testing.T) {
	// Summarizing can take much longer than a normal call; the server holds
	// the request open until it is done. A slow response must not be turned
	// into a failure by the ordinary client timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	c.HTTP.Timeout = 30 * time.Millisecond // stands in for the usual 30s
	if _, err := c.GetStation(1); err == nil {
		t.Fatal("a normal call should hit the short timeout in this setup")
	}
	if err := c.CompactStation(1); err != nil {
		t.Fatalf("compact must not use the ordinary timeout: %v", err)
	}
}

func TestListStationsSkipsHiddenAndKeepsServerOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"id":3,"name":"c"},{"id":1,"name":"a","hidden":true},{"id":2,"name":"b","hidden":false}]`)
	}))
	defer srv.Close()
	got, err := New(srv.URL, "").ListStations()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 3 || got[1].ID != 2 {
		t.Fatalf("got %+v, want visible stations 3,2 in server order", got)
	}
}

func TestUsageAndProvidersShapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stations/1/usage":
			w.Write([]byte(`{"type":"usage","sessionID":"s","tokens":{"input":1,"output":2,"reasoning":3,"cacheRead":4,"cacheWrite":5,"ctx":7100},"cost":0.5,"contextLimit":12000}`))
		case "/stations/2/usage":
			w.Write([]byte(`{"type":"usage","sessionID":"s","cost":0,"contextLimit":12000}`))
		case "/providers":
			w.Write([]byte(`{"providers":[{"id":"local","name":"Local","baseURL":"http://x/v1","models":{"m":{"id":"m","name":"M","limit":{"context":32768,"output":4096}}}}],"default":{}}`))
		case "/stations/1/status":
			w.Write([]byte(`{"status":"busy"}`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	u, err := c.Usage(1)
	if err != nil || u.Context() != 7100 || u.ContextLimit != 12000 || u.Tokens.CacheRead != 4 || u.Cost != 0.5 {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	if u, err := c.Usage(2); err != nil || u.Tokens != nil || u.Context() != 0 {
		t.Fatalf("usage before the first step = %+v, %v", u, err)
	}
	p, err := c.Providers()
	if err != nil || p.Providers[0].BaseURL != "http://x/v1" || p.Providers[0].Models["m"].Limit.Context != 32768 {
		t.Fatalf("providers = %+v, %v", p, err)
	}
	if st, err := c.RunStatus(1); err != nil || st != "busy" {
		t.Fatalf("status = %q, %v", st, err)
	}
}

func TestLimits(t *testing.T) {
	var put map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/limits" {
			w.WriteHeader(404)
			return
		}
		if r.Method == "PUT" {
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &put)
			w.WriteHeader(204)
			return
		}
		if r.URL.Query().Get("provider_id") != "local" || r.URL.Query().Get("model_id") != "m" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Write([]byte(`[{"provider_id":"local","model_id":"m","name":"M","context":16384,"output":4096,"detected":32768,"override":{"context":16384,"output":null},"source":"override"}]`))
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	ls, err := c.ListLimits("local", "m")
	if err != nil || len(ls) != 1 || ls[0].Source != "override" || *ls[0].Override.Context != 16384 || ls[0].Override.Output != nil || ls[0].Detected != 32768 {
		t.Fatalf("limits = %+v, %v", ls, err)
	}
	n := 20000
	if err := c.SetLimit("local", "m", &n, nil); err != nil {
		t.Fatal(err)
	}
	if put["provider_id"] != "local" || put["model_id"] != "m" || put["context"] != float64(20000) {
		t.Fatalf("put = %v", put)
	}
	if v, ok := put["output"]; !ok || v != nil {
		t.Fatalf("output must be sent as null, got %v", put)
	}
	if err := c.SetLimit("local", "m", nil, nil); err != nil || put["context"] != nil {
		t.Fatalf("clear: put = %v, %v", put, err)
	}
}
