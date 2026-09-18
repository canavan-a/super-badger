// Package api is super-badger's own simplified HTTP surface: Station CRUD
// plus thin passthroughs to opencode for providers/models, so a caller never
// has to speak opencode's session/provider/model API directly.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"main/database"
	"main/metrics"
	"main/mullvad"
	"main/notify"
	"main/opencode"
	"main/station"
)

func NewRouter(svc *station.Service, oc *opencode.Client, mv *mullvad.Client, broker *opencode.EventBroker, db *gorm.DB, mm *metrics.Manager, hub *notify.Hub) *gin.Engine {
	r := gin.Default()

	// Auth is opt-in (see RequireAuth) so any origin is fine here too — the
	// mobile app's web target runs on a different origin/port (e.g. Vite on
	// :5173) than this API (:8080), and without CORS the browser's preflight
	// OPTIONS request 404s before the real request ever goes out.
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"*"}
	corsConfig.AllowHeaders = []string{"Content-Type", "Authorization"}
	r.Use(cors.New(corsConfig))
	r.Use(RequireAuth(db))

	// Liveness of superbadger itself — deliberately not touching opencode or
	// the DB, so the app can tell "superbadger is down" apart from "opencode
	// is down" (station reachability, below) or "a station's model is down".
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.POST("/stations", createStation(svc))
	r.GET("/stations", listStations(svc))
	r.GET("/stations/:id", getStation(svc))
	r.PATCH("/stations/:id", updateStation(svc))
	r.DELETE("/stations/:id", deleteStation(svc))
	r.POST("/stations/:id/prompt", promptStation(svc))
	r.POST("/stations/:id/reset", resetStation(svc))
	r.POST("/stations/:id/compact", compactStation(svc))
	r.POST("/stations/:id/abort", abortStation(svc))
	r.GET("/stations/:id/usage", stationUsage(svc))
	r.GET("/stations/:id/ws", stationWS(svc, broker))
	r.GET("/stations/:id/history", stationHistory(svc))
	r.GET("/stations/:id/datapoints", listStationDataPoints(db))
	r.PUT("/stations/:id/datapoints/:key/settings", updateDataPointSettings(db))
	r.POST("/stations/:id/datapoints/:key/hide", hideDataPoint(db))
	r.PUT("/stations/:id/datapoints/reorder", reorderDataPoints(db))
	r.GET("/stations/:id/datapoints/:key/history", stationDataPointHistory(db))
	r.GET("/notifications/ws", notificationsWS(svc, broker, hub))

	r.GET("/metric-sources", listMetricSources(db))
	r.POST("/metric-sources", createMetricSource(db, mm))
	r.PUT("/metric-sources/:id", updateMetricSource(db, mm))
	r.DELETE("/metric-sources/:id", deleteMetricSource(db, mm))

	r.GET("/providers", listProviders(oc))
	r.GET("/models", listModels(oc))
	r.POST("/providers/test", testProviderConnection())

	r.GET("/mullvad/status", mullvadStatus(mv))
	r.POST("/mullvad/connect", mullvadConnect(mv))
	r.POST("/mullvad/disconnect", mullvadDisconnect(mv))
	r.POST("/mullvad/configure", mullvadConfigure(mv))
	r.POST("/mullvad/location", mullvadSetLocation(mv))
	r.GET("/mullvad/relays", mullvadListRelays(mv))
	r.POST("/mullvad/lan", mullvadSetLAN(mv))

	return r
}

func parseID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid station id"})
		return 0, false
	}
	return uint(id), true
}

func createStation(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var p station.CreateParams
		if err := c.ShouldBindJSON(&p); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		st, err := svc.Create(c.Request.Context(), p)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "station": st})
			return
		}
		reachable := svc.Reachable(c.Request.Context(), st.ProviderID)
		c.JSON(http.StatusCreated, toStationOut(st, reachable))
	}
}

// stationOut adds a live-computed `reachable` field to a Station response —
// see station.Service.Reachable for why this can't just be st.Status
// (status only reflects whether a session was created, never whether the
// model behind it is still up right now) — and decodes TopBarActions'
// JSON-text column into a plain array for the app.
type stationOut struct {
	database.Station
	Reachable     bool     `json:"reachable"`
	TopBarActions []string `json:"top_bar_actions"`
}

func toStationOut(st database.Station, reachable bool) stationOut {
	actions := []string{}
	if st.TopBarActions != nil && *st.TopBarActions != "" {
		_ = json.Unmarshal([]byte(*st.TopBarActions), &actions)
	}
	return stationOut{Station: st, Reachable: reachable, TopBarActions: actions}
}

func listStations(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		stations, err := svc.List()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		providerIDs := make([]string, len(stations))
		for i, st := range stations {
			providerIDs[i] = st.ProviderID
		}
		reachable := svc.ReachableAll(c.Request.Context(), providerIDs)

		out := make([]stationOut, len(stations))
		for i, st := range stations {
			out[i] = toStationOut(st, reachable[st.ProviderID])
		}
		c.JSON(http.StatusOK, out)
	}
}

func getStation(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		st, err := svc.Get(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		reachable := svc.Reachable(c.Request.Context(), st.ProviderID)
		c.JSON(http.StatusOK, toStationOut(st, reachable))
	}
}

// stationColors is the fixed preset the app offers in its color picker;
// enforced here too so a bad/arbitrary value can't be persisted.
var stationColors = map[string]bool{
	"#4C8BF5": true,
	"#34C759": true,
	"#FF9500": true,
	"#FF3B30": true,
	"#AF52DE": true,
	"#8E8E93": true,
}

// topBarActionKeys are the fixed header buttons/badges that can be opted
// into the top bar (see stationOut.TopBarActions) - "data" isn't included
// since that's always shown (it's the only way to reach this config).
// "tokens" is a badge (a label:value pill, like a data point) rather than a
// button — it shares this same opt-in list/column since both are just
// "things the owner chose to show on the top bar".
var topBarActionKeys = map[string]bool{
	"compact": true,
	"reset":   true,
	"delete":  true,
	"tokens":  true,
}

func updateStation(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		var body struct {
			Name          *string  `json:"name"`
			Alias         *string  `json:"alias"`
			Color         *string  `json:"color"`
			TopBarActions []string `json:"top_bar_actions"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if body.Color != nil && !stationColors[*body.Color] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "color must be one of the preset options"})
			return
		}
		for _, a := range body.TopBarActions {
			if !topBarActionKeys[a] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown top bar action: " + a})
				return
			}
		}
		updates := map[string]any{}
		if body.Name != nil {
			updates["name"] = *body.Name
		}
		if body.Alias != nil {
			alias := *body.Alias
			if alias == "" {
				updates["alias"] = nil
			} else {
				updates["alias"] = alias
			}
		}
		if body.Color != nil {
			updates["color"] = *body.Color
		}
		if body.TopBarActions != nil {
			encoded, _ := json.Marshal(body.TopBarActions)
			updates["top_bar_actions"] = string(encoded)
		}
		st, err := svc.Update(id, updates)
		if err != nil {
			if isUniqueConstraintErr(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "name or alias already in use"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		reachable := svc.Reachable(c.Request.Context(), st.ProviderID)
		c.JSON(http.StatusOK, toStationOut(st, reachable))
	}
}

// isUniqueConstraintErr reports whether err came from a UNIQUE index
// violation - checked by message rather than a driver-specific error type so
// this doesn't need to import the sqlite driver package directly.
func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT")
}

func deleteStation(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		if err := svc.Delete(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func promptStation(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		var body struct {
			Text string `json:"text" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		resp, err := svc.Prompt(c.Request.Context(), id, body.Text)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", resp)
	}
}

func resetStation(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		st, err := svc.Reset(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "station": st})
			return
		}
		// Without this, the response decodes with reachable missing/false on
		// the app side (Station always expects the field) — right after a
		// successful reset (session is fine, model is fine), the chat screen
		// would flash the "isn't responding" hint purely because this
		// endpoint forgot to compute the field getStation/listStations
		// already do.
		reachable := svc.Reachable(c.Request.Context(), st.ProviderID)
		c.JSON(http.StatusOK, toStationOut(st, reachable))
	}
}

// compactStation triggers opencode's conversation-summarization for a
// Station's session — the same thing the CLI's /compact command does (see
// opencode.Client.Compact).
func compactStation(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		if err := svc.Compact(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// abortStation cancels a Station's session's in-flight turn — the same thing
// the CLI's Escape/Ctrl-C does mid-response (see station.Service.Abort).
func abortStation(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		if err := svc.Abort(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// stationUsage reports a Station's session's current token/cost accounting
// (see station.Service.TokenUsage) — the same figures the CLI's status line
// shows.
func stationUsage(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		usage, err := svc.TokenUsage(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, usage)
	}
}

// stationHistory returns the small cached transcript for a Station's current
// session (empty array if none, including after a reset — see
// station.Service.History), so a client that navigates away and back can
// restore recent chat instead of starting blank.
func stationHistory(svc *station.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		events, err := svc.History(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if events == nil {
			events = []opencode.Event{}
		}
		c.JSON(http.StatusOK, events)
	}
}

func listProviders(oc *opencode.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := oc.ListProviders(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", raw)
	}
}

func listModels(oc *opencode.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := oc.ListModels(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", raw)
	}
}

func testProviderConnection() gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			BaseURL string `json:"base_url" binding:"required"`
			APIKey  string `json:"api_key"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}
		if err := station.TestConnection(c.Request.Context(), body.BaseURL, body.APIKey); err != nil {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func mullvadStatus(mv *mullvad.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		out, err := mv.Status(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"output": out})
	}
}

func mullvadConnect(mv *mullvad.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		out, err := mv.Connect(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"output": out})
	}
}

func mullvadDisconnect(mv *mullvad.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		out, err := mv.Disconnect(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"output": out})
	}
}

func mullvadConfigure(mv *mullvad.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			AccountToken string `json:"account_token" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		out, err := mv.Login(c.Request.Context(), body.AccountToken)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"output": out})
	}
}

func mullvadSetLocation(mv *mullvad.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Country  string `json:"country" binding:"required"`
			City     string `json:"city"`
			Hostname string `json:"hostname"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		out, err := mv.SetLocation(c.Request.Context(), body.Country, body.City, body.Hostname)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"output": out})
	}
}

func mullvadListRelays(mv *mullvad.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		out, err := mv.ListRelays(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"output": out})
	}
}

func mullvadSetLAN(mv *mullvad.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Allow bool `json:"allow"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		out, err := mv.SetLAN(c.Request.Context(), body.Allow)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"output": out})
	}
}
