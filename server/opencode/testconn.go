package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// probeModels GETs the model endpoint's own OpenAI-compatible /models route
// directly — bypassing opencode entirely — and reports the HTTP status, or
// an error when no response came back at all (bad URL, refused, timeout).
func probeModels(ctx context.Context, baseURL, apiKey string) (string, int, error) {
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return url, 0, fmt.Errorf("build request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return url, 0, fmt.Errorf("connect to %s: %w", url, err)
	}
	resp.Body.Close()
	return url, resp.StatusCode, nil
}

// TestModelConnection validates a base URL/API key pair against the model
// endpoint, so a user can check both before ever creating a Station or an
// opencode session against it. Strict: a rejected key (401/403) is a failure.
func TestModelConnection(ctx context.Context, baseURL, apiKey string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	url, status, err := probeModels(ctx, baseURL, apiKey)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("%s responded with status %d", url, status)
	}
	return nil
}

// ModelEndpointAlive reports whether a model server is up and answering at
// baseURL — the liveness behind a Station's "reachable". Unlike
// TestModelConnection it counts 401/403 as alive: the server answered, it
// just wants credentials this check may not have (opencode can keep a
// provider's key in its own auth store, outside the provider options this
// check reads, while chatting through that provider works fine).
func ModelEndpointAlive(ctx context.Context, baseURL, apiKey string) bool {
	_, status, err := probeModels(ctx, baseURL, apiKey)
	if err != nil {
		return false
	}
	return status < 300 || status == http.StatusUnauthorized || status == http.StatusForbidden
}

// EngineContext asks the llama.cpp or Strata server behind baseURL for its
// context size (GET <origin>/props, default_generation_settings.n_ctx) — the
// same probe the super-badger opencode plugin runs. 0 when the server doesn't
// say (not a llama.cpp/Strata server, down, or Strata still starting).
func EngineContext(ctx context.Context, baseURL, apiKey string) int {
	u := strings.TrimRight(baseURL, "/")
	u = strings.TrimSuffix(u, "/v1") + "/props"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return 0
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var p struct {
		Settings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
		NCtx int `json:"n_ctx"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&p) != nil {
		return 0
	}
	n := p.Settings.NCtx
	if n == 0 {
		n = p.NCtx
	}
	if n < 1024 {
		return 0
	}
	return n
}
