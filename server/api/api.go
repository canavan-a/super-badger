// Package api is super-badger's own simplified HTTP surface: Station CRUD
// plus thin passthroughs to opencode for providers/models, so a caller never
// has to speak opencode's session/provider/model API directly.
package api

import (
	"net/http"
	"strconv"

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

	// No auth yet (see README), so any origin is fine for now — the mobile
	// app's web target runs on a different origin/port (e.g. Vite on :5173)
	// than this API (:8080), and without CORS the browser's preflight
	// OPTIONS request 404s before the real request ever goes out.
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"*"}
	corsConfig.AllowHeaders = []string{"Content-Type"}
	r.Use(cors.New(corsConfig))

	// Liveness of superbadger itself — deliberately not touching opencode or
	// the DB, so the app can tell "superbadger is down" apart from "opencode
	// is down" (station reachability, below) or "a station's model is down".
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.POST("/stations", createStation(svc))
	r.GET("/stations", listStations(svc))
	r.GET("/stations/:id", getStation(svc))
	r.DELETE("/stations/:id", deleteStation(svc))
	r.POST("/stations/:id/prompt", promptStation(svc))
	r.POST("/stations/:id/reset", resetStation(svc))
	r.POST("/stations/:id/compact", compactStation(svc))
	r.GET("/stations/:id/usage", stationUsage(svc))
	r.GET("/stations/:id/ws", stationWS(svc, broker))
	r.GET("/stations/:id/history", stationHistory(svc))
	r.GET("/stations/:id/datapoints", listStationDataPoints(db))
	r.PUT("/stations/:id/datapoints/:key/settings", updateDataPointSettings(db))
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
		c.JSON(http.StatusCreated, stationOut{Station: st, Reachable: reachable})
	}
}

// stationOut adds a live-computed `reachable` field to a Station response —
// see station.Service.Reachable for why this can't just be st.Status
// (status only reflects whether a session was created, never whether the
// model behind it is still up right now).
type stationOut struct {
	database.Station
	Reachable bool `json:"reachable"`
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
			out[i] = stationOut{Station: st, Reachable: reachable[st.ProviderID]}
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
		c.JSON(http.StatusOK, stationOut{Station: st, Reachable: reachable})
	}
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
		c.JSON(http.StatusOK, stationOut{Station: st, Reachable: reachable})
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
