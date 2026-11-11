package api

import (
	"context"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"main/database"
	"main/opencode"
)

// Context limits are per provider/model, not per station: the super-badger
// opencode plugin detects each model's window from its engine and applies
// any override stored here (GET /internal/limits), live, no restart. These
// handlers show what opencode is actually using and edit the overrides.

type limitPair struct {
	Context *int `json:"context"`
	Output  *int `json:"output"`
}

type limitOut struct {
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
	Name       string `json:"name"`
	// Context/Output are what opencode compacts against right now.
	Context int `json:"context"`
	Output  int `json:"output"`
	// Detected is the engine's own n_ctx (llama.cpp/Strata /props), 0 when
	// it doesn't report one.
	Detected int        `json:"detected"`
	Override *limitPair `json:"override"`
	// Source is where Context comes from: override, detected, or default
	// (opencode's own guess — usually wrong for a local model).
	Source string `json:"source"`
}

// probeTimeout bounds one engine /props probe.
const probeTimeout = 1500 * time.Millisecond

func modelLimits(ctx context.Context, oc *opencode.Client, db *gorm.DB) ([]limitOut, error) {
	var (
		providers []opencode.Provider
		models    []opencode.Model
		pErr      error
		mErr      error
		wg        sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); providers, pErr = oc.Providers(ctx) }()
	go func() { defer wg.Done(); models, mErr = oc.Models(ctx) }()
	wg.Wait()
	if pErr != nil {
		return nil, pErr
	}
	if mErr != nil {
		return nil, mErr
	}
	overrides, err := database.ListModelLimits(db)
	if err != nil {
		return nil, err
	}
	byKey := map[[2]string]database.ModelLimit{}
	for _, o := range overrides {
		byKey[[2]string{o.ProviderID, o.ModelID}] = o
	}

	// One probe per engine, in parallel.
	detected := map[string]int{}
	var mu sync.Mutex
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	for _, p := range providers {
		if p.Settings.BaseURL == "" || p.ID == "opencode" {
			continue
		}
		wg.Add(1)
		go func(p opencode.Provider) {
			defer wg.Done()
			n := opencode.EngineContext(pctx, p.Settings.BaseURL, p.Settings.APIKey)
			mu.Lock()
			detected[p.ID] = n
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	out := []limitOut{}
	for _, m := range models {
		if !m.Enabled && m.Status != "active" {
			continue
		}
		l := limitOut{ProviderID: m.ProviderID, ModelID: m.ID, Name: m.Name, Context: m.Limit.Context, Output: m.Limit.Output, Detected: detected[m.ProviderID], Source: "default"}
		if o, ok := byKey[[2]string{m.ProviderID, m.ID}]; ok {
			l.Override = &limitPair{Context: o.Context, Output: o.Output}
		}
		switch {
		case l.Override != nil && l.Override.Context != nil:
			l.Source = "override"
		case l.Detected > 0:
			l.Source = "detected"
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out, nil
}

func listLimits(oc *opencode.Client, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		out, err := modelLimits(c.Request.Context(), oc, db)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if p := c.Query("provider_id"); p != "" {
			filtered := out[:0]
			for _, l := range out {
				if l.ProviderID == p && (c.Query("model_id") == "" || l.ModelID == c.Query("model_id")) {
					filtered = append(filtered, l)
				}
			}
			out = filtered
		}
		c.JSON(http.StatusOK, out)
	}
}

// setLimit stores or clears an override:
// {provider_id, model_id, context: N|null, output: N|null}. Both null
// removes it. opencode picks it up within the plugin's poll interval.
func setLimit(db *gorm.DB, broker *opencode.EventBroker) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			ProviderID string `json:"provider_id" binding:"required"`
			ModelID    string `json:"model_id" binding:"required"`
			Context    *int   `json:"context"`
			Output     *int   `json:"output"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if body.Context != nil && (*body.Context < 1024 || *body.Context > 10_000_000) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "context must be between 1024 and 10000000 tokens"})
			return
		}
		if body.Output != nil && *body.Output < 256 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "output must be at least 256 tokens"})
			return
		}
		if body.Context != nil && body.Output != nil && *body.Output >= *body.Context {
			c.JSON(http.StatusBadRequest, gin.H{"error": "output must be smaller than context"})
			return
		}
		err := database.SetModelLimit(db, database.ModelLimit{ProviderID: body.ProviderID, ModelID: body.ModelID, Context: body.Context, Output: body.Output})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		broker.InvalidateLimits()
		c.Status(http.StatusNoContent)
	}
}

// internalLimits is what the opencode plugin polls:
// {overrides: {"provider/model": {context?, output?}}}. Served only to
// loopback clients (opencode runs on the same host), without a token.
func internalLimits(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := database.ListModelLimits(db)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		overrides := map[string]gin.H{}
		for _, r := range rows {
			o := gin.H{}
			if r.Context != nil {
				o["context"] = *r.Context
			}
			if r.Output != nil {
				o["output"] = *r.Output
			}
			overrides[r.ProviderID+"/"+r.ModelID] = o
		}
		c.JSON(http.StatusOK, gin.H{"overrides": overrides})
	}
}

// isLocal reports whether the request came straight from this host (not
// through a proxy header — gin's RemoteIP reads the socket). That is a
// loopback address, or one of the host's own addresses: when the server
// listens on one specific IP (a LAN or Tailscale address), the plugin has to
// connect to that IP, and the request's source is then that IP, not
// 127.0.0.1.
func isLocal(c *gin.Context) bool {
	ip := net.ParseIP(c.RemoteIP())
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	return isHostAddr(ip, net.InterfaceAddrs)
}

// isHostAddr reports whether ip is assigned to one of this host's interfaces.
func isHostAddr(ip net.IP, addrs func() ([]net.Addr, error)) bool {
	as, err := addrs()
	if err != nil {
		return false
	}
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// providersOut keeps GET /providers' original shape ({providers: [{id,
// name, models: {id: {id, name}}}]}) and adds each model's limit.
type providerOut struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	BaseURL string              `json:"baseURL,omitempty"`
	Models  map[string]modelOut `json:"models"`
}

type modelOut struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

func listProviders(oc *opencode.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		providers, err := oc.Providers(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		models, err := oc.Models(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		byID := map[string]*providerOut{}
		out := []*providerOut{}
		for _, p := range providers {
			po := &providerOut{ID: p.ID, Name: p.Name, BaseURL: p.Settings.BaseURL, Models: map[string]modelOut{}}
			byID[p.ID] = po
			out = append(out, po)
		}
		for _, m := range models {
			po := byID[m.ProviderID]
			if po == nil || (!m.Enabled && m.Status != "active") {
				continue
			}
			mo := modelOut{ID: m.ID, Name: m.Name}
			mo.Limit.Context, mo.Limit.Output = m.Limit.Context, m.Limit.Output
			po.Models[m.ID] = mo
		}
		// Providers without a usable model can't back a station.
		kept := out[:0]
		for _, p := range out {
			if len(p.Models) > 0 {
				kept = append(kept, p)
			}
		}
		c.JSON(http.StatusOK, gin.H{"providers": kept, "default": gin.H{}})
	}
}

func listModels(oc *opencode.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		models, err := oc.Models(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, models)
	}
}
