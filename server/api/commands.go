// Command handlers for the Super Badger Station Standard API's `commands`
// section (see docs/command-spec.md). GET /commands returns the command
// list captured by the most recent poll, joined with the owning station's
// display identity; POST /commands/invoke proxies a POST to the resolved
// target and streams the response through chunk-by-chunk. Streaming
// matters because commands are actions, not reads — toggling a model on or
// off can take up to a minute, and an endpoint that reports progress (or
// just takes its time) shouldn't have to look like one opaque wait.
package api

import (
	"net/http"
	"net/url"
	"sort"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"main/database"
)

// commandClient has no timeout of its own beyond the request context:
// a command may legitimately stream for a minute, while gin's request
// context cancels the upstream call if the client disconnects.
var commandClient = &http.Client{}

// commandOut is one command row joined with its station's display identity
// (name/color, and the owner's ordering) so the app can render the
// Commands screen without a second round trip.
type commandOut struct {
	StationID    uint   `json:"station_id"`
	StationName  string `json:"station_name"`
	StationColor string `json:"station_color"`
	Path         string `json:"path"`
	Label        string `json:"label"`
	URL          string `json:"url"`
}

func listCommands(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		stations, err := database.ListStations(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		commands, err := database.ListStationCommands(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Same ordering as the drawer's station list (position, then id),
		// then by path within a station.
		rank := make(map[uint]int, len(stations))
		info := make(map[uint]database.Station, len(stations))
		for i, st := range stations {
			rank[st.ID] = i
			info[st.ID] = st
		}

		type entry = commandOut
		out := make([]entry, 0, len(commands))
		for _, cmd := range commands {
			st, ok := info[cmd.StationID]
			if !ok || st.Hidden {
				continue // hidden stations are off the drawer, so their
				// commands shouldn't surface either
			}
			out = append(out, entry{
				StationID:    cmd.StationID,
				StationName:  st.Name,
				StationColor: st.Color,
				Path:         cmd.Path,
				Label:        cmd.Label,
				URL:          cmd.URL,
			})
		}
		sort.SliceStable(out, func(i, j int) bool {
			if rank[out[i].StationID] != rank[out[j].StationID] {
				return rank[out[i].StationID] < rank[out[j].StationID]
			}
			return out[i].Path < out[j].Path
		})
		c.JSON(http.StatusOK, out)
	}
}

// invokeCommand proxies one POST to a station's advertised command URL.
// The upstream response is streamed through rather than buffered, so a
// slow command (model toggles run up to ~60s) can stream progress events
// that the client sees as they happen; a plain JSON response passes
// through unchanged.
func invokeCommand(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			StationID uint   `json:"station_id" binding:"required"`
			Path      string `json:"path" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		commands, err := database.ListStationCommands(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		var target *database.StationCommand
		for i := range commands {
			if commands[i].StationID == body.StationID && commands[i].Path == body.Path {
				target = &commands[i]
				break
			}
		}
		if target == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such command for this station"})
			return
		}

		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target.URL, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Re-attach the metric source's API key when the command URL shares
		// its origin — the client authenticates to *this* server, and this
		// server authenticates onward.
		if key := metricAPIKeyForOrigin(db, target.URL); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}

		upstream, err := commandClient.Do(req)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "command target unreachable: " + err.Error()})
			return
		}
		defer upstream.Body.Close()

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

// metricAPIKeyForOrigin finds the API key of the enabled metric source
// whose origin (scheme://host[:port]) matches the command URL, so an
// invoke can authenticate to the target the same way the poller does.
func metricAPIKeyForOrigin(db *gorm.DB, commandURL string) string {
	srcs, err := database.ListMetricSources(db)
	if err != nil {
		return ""
	}
	target, err := url.Parse(commandURL)
	if err != nil {
		return ""
	}
	for _, s := range srcs {
		if !s.Enabled || s.APIKey == nil {
			continue
		}
		base, err := url.Parse(s.URL)
		if err != nil {
			continue
		}
		if base.Scheme == target.Scheme && base.Host == target.Host {
			return *s.APIKey
		}
	}
	return ""
}
