// Command handlers for the commands each metric source serves on its
// sibling commands endpoint (see docs/command-spec.md). Commands belong to
// the source as a whole, not to any station. GET /commands returns the
// lists captured by the most recent polls, joined with each source's name;
// POST /commands/invoke proxies a POST to the command's URL and streams the
// response through chunk-by-chunk. Streaming matters because commands are
// actions, not reads — toggling a model on or off can take up to a minute,
// and an endpoint that reports progress (or just takes its time) shouldn't
// have to look like one opaque wait.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"main/database"
	"main/metrics"
)

// commandClient has no timeout of its own beyond the request context:
// a command may legitimately stream for a minute, while gin's request
// context cancels the upstream call if the client disconnects.
var commandClient = &http.Client{}

// commandOut is one command row joined with its source's name so the app
// can group and label the Commands screen without a second round trip.
type commandOut struct {
	SourceID   uint                     `json:"source_id"`
	SourceName string                   `json:"source_name"`
	Path       string                   `json:"path"`
	Label      string                   `json:"label"`
	URL        string                   `json:"url"`
	Group      string                   `json:"group"`
	Active     bool                     `json:"active"`
	Confirm    string                   `json:"confirm"`
	Options    []database.CommandOption `json:"options,omitempty"`
}

// enabledSources maps id → source for every enabled metric source.
// Commands from a disabled source are hidden: its poller is stopped, so its
// list is stale and the owner has switched it off anyway.
func enabledSources(db *gorm.DB) (map[uint]database.MetricSource, error) {
	srcs, err := database.ListMetricSources(db)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]database.MetricSource, len(srcs))
	for _, s := range srcs {
		if s.Enabled {
			out[s.ID] = s
		}
	}
	return out, nil
}

func listCommands(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		sources, err := enabledSources(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Already ordered by source, then the endpoint's own order.
		commands, err := database.ListCommands(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		out := make([]commandOut, 0, len(commands))
		for _, cmd := range commands {
			src, ok := sources[cmd.SourceID]
			if !ok {
				continue
			}
			out = append(out, commandOut{
				SourceID:   cmd.SourceID,
				SourceName: src.Name,
				Path:       cmd.Path,
				Label:      cmd.Label,
				URL:        cmd.URL,
				Group:      cmd.Group,
				Active:     cmd.Active,
				Confirm:    cmd.Confirm,
				Options:    cmd.OptionList(),
			})
		}
		c.JSON(http.StatusOK, out)
	}
}

// invokeCommand proxies one POST to a source's advertised command URL.
// The upstream response is streamed through rather than buffered, so a
// slow command (model toggles run up to ~60s) can stream progress events
// that the client sees as they happen; a plain JSON response passes
// through unchanged.
func invokeCommand(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			SourceID uint   `json:"source_id" binding:"required"`
			Path     string `json:"path" binding:"required"`
			// Option is the chosen value of a picker command; empty for a
			// plain one.
			Option string `json:"option"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		sources, err := enabledSources(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		src, ok := sources[body.SourceID]
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such metric source"})
			return
		}

		commands, err := database.ListCommands(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		var target *database.Command
		for i := range commands {
			if commands[i].SourceID == body.SourceID && commands[i].Path == body.Path {
				target = &commands[i]
				break
			}
		}
		if target == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such command for this source"})
			return
		}

		// A picker command must be run with one of its own options, and a
		// plain command with none, so the endpoint never sees a value it
		// didn't offer.
		var reqBody io.Reader
		if opts := target.OptionList(); len(opts) > 0 {
			valid := false
			for _, o := range opts {
				if o.Value == body.Option {
					valid = true
					break
				}
			}
			if !valid {
				c.JSON(http.StatusBadRequest, gin.H{"error": "option must be one of the command's options"})
				return
			}
			b, _ := json.Marshal(gin.H{"option": body.Option})
			reqBody = bytes.NewReader(b)
		} else if body.Option != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "this command takes no option"})
			return
		}

		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target.URL, reqBody)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		// The client authenticates to *this* server, and this server
		// authenticates onward with the source's key — but only when the
		// command URL is on the source's own origin, so an endpoint can't
		// steer its key to some other host.
		if src.APIKey != nil && *src.APIKey != "" && sameOrigin(src.URL, target.URL) {
			req.Header.Set("Authorization", "Bearer "+*src.APIKey)
		}

		upstream, err := commandClient.Do(req)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "command target unreachable: " + err.Error()})
			return
		}
		defer upstream.Body.Close()
		// Once the command's output ends, re-read the source's command list
		// before the response completes: the app reloads the list as soon as
		// its request finishes, and should see the state the command just
		// produced (e.g. the newly active mode), not the last tick's.
		// Background context: still worth doing if the app hung up.
		defer metrics.RefreshCommands(context.Background(), db, src)

		w := c.Writer
		if ct := upstream.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(upstream.StatusCode)

		flusher, canFlush := w.(http.Flusher)
		buf := make([]byte, 32*1024)
		for {
			n, err := upstream.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				if canFlush {
					flusher.Flush()
				}
			}
			if err != nil {
				return
			}
		}
	}
}

// sameOrigin reports whether a and b share scheme://host[:port].
func sameOrigin(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return ua.Scheme == ub.Scheme && ua.Host == ub.Host
}
