package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCommandsURL(t *testing.T) {
	cases := map[string]string{
		"http://box:9000/metrics":     "http://box:9000/commands",
		"http://box:9000/":            "http://box:9000/commands",
		"http://box:9000":             "http://box:9000/commands",
		"http://box:9000/api/metrics": "http://box:9000/api/commands",
	}
	for in, want := range cases {
		got, err := NewHTTPSource("s", in, "").CommandsURL()
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got.String() != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestPollCommands(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/commands" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`[
			{"path": "model/toggle", "label": "Toggle model", "url": "/model/toggle"},
			{"path": "restart", "url": "http://other:1/restart"},
			{"path": "model/toggle", "url": "/dupe"},
			{"path": "", "url": "/no-path"},
			{"path": "no-url"}
		]`))
	}))
	defer srv.Close()

	cmds, err := NewHTTPSource("s", srv.URL+"/api/metrics", "k").PollCommands(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer k" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if len(cmds) != 2 {
		t.Fatalf("got %d commands, want 2: %+v", len(cmds), cmds)
	}
	if cmds[0].Path != "model/toggle" || cmds[0].URL != srv.URL+"/model/toggle" || cmds[0].Position != 0 {
		t.Errorf("cmd 0 = %+v", cmds[0])
	}
	if cmds[1].Path != "restart" || cmds[1].URL != "http://other:1/restart" || cmds[1].Position != 1 {
		t.Errorf("cmd 1 = %+v", cmds[1])
	}

	_, err = NewHTTPSource("s", srv.URL+"/metrics", "").PollCommands(context.Background())
	if !errors.Is(err, errNoCommands) {
		t.Errorf("404 err = %v, want errNoCommands", err)
	}
}

func TestPollCommandsDisplayHints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"path": "double", "url": "/c/double", "group": "Mode", "active": true, "confirm": "Sure?"},
			{"path": "model", "url": "/c/model", "group": "Model", "options": [
				{"value": "a", "label": "A", "active": true},
				{"value": "", "label": "no value"},
				{"value": "a", "label": "dupe"},
				{"value": "b"}
			]},
			{"path": "empty-picker", "url": "/c/x", "options": [{"value": ""}]}
		]`))
	}))
	defer srv.Close()

	cmds, err := NewHTTPSource("s", srv.URL+"/metrics", "").PollCommands(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 3 {
		t.Fatalf("got %d commands", len(cmds))
	}
	d := cmds[0]
	if d.Group != "Mode" || !d.Active || d.Confirm != "Sure?" || d.Options != nil {
		t.Errorf("double = %+v", d)
	}
	opts := cmds[1].OptionList()
	if len(opts) != 2 || opts[0].Value != "a" || opts[0].Label != "A" || !opts[0].Active || opts[1].Value != "b" {
		t.Errorf("model options = %+v", opts)
	}
	if cmds[2].Options != nil {
		t.Errorf("picker with no valid options should be plain, got %s", *cmds[2].Options)
	}
}
