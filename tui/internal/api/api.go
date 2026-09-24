// Package api is the REST client for the superbadger server, mirroring
// app/src/api.ts.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var StationColors = []string{"#4C8BF5", "#34C759", "#FF9500", "#FF3B30", "#AF52DE", "#8E8E93"}

var TopBarActionKeys = []string{"compact", "reset", "delete", "tokens"}

type Station struct {
	ID                uint     `json:"id"`
	Name              string   `json:"name"`
	Alias             *string  `json:"alias"`
	Color             string   `json:"color"`
	TopBarActions     []string `json:"top_bar_actions"`
	ProviderID        string   `json:"provider_id"`
	ModelID           string   `json:"model_id"`
	Directory         string   `json:"directory"`
	Agent             string   `json:"agent"`
	OpencodeSessionID string   `json:"opencode_session_id"`
	Status            string   `json:"status"`
	Reachable         bool     `json:"reachable"`
}

func (s Station) HasAction(a string) bool {
	for _, x := range s.TopBarActions {
		if x == a {
			return true
		}
	}
	return false
}

type TokenUsage struct {
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Reasoning  int     `json:"reasoning"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Cost       float64 `json:"cost"`
}

// Context is the active context size shown on the top bar (see
// StationDetailScreen.tsx): everything but reasoning.
func (u TokenUsage) Context() int { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

type DataPoint struct {
	Key                string  `json:"key"`
	Label              string  `json:"label"`
	Value              float64 `json:"value"`
	Decimals           int     `json:"decimals"`
	UpdatedAt          string  `json:"updated_at"`
	ShowOnTopBar       bool    `json:"show_on_top_bar"`
	ThresholdEnabled   bool    `json:"threshold_enabled"`
	ThresholdValue     float64 `json:"threshold_value"`
	ThresholdDirection string  `json:"threshold_direction"`
	Order              int     `json:"order"`
}

type DataPointSettings struct {
	Label              string  `json:"label"`
	Decimals           int     `json:"decimals"`
	ShowOnTopBar       bool    `json:"show_on_top_bar"`
	ThresholdEnabled   bool    `json:"threshold_enabled"`
	ThresholdValue     float64 `json:"threshold_value"`
	ThresholdDirection string  `json:"threshold_direction"`
}

func (d DataPoint) Settings() DataPointSettings {
	return DataPointSettings{d.Label, d.Decimals, d.ShowOnTopBar, d.ThresholdEnabled, d.ThresholdValue, d.ThresholdDirection}
}

type MetricSource struct {
	ID                  uint    `json:"id"`
	Name                string  `json:"name"`
	URL                 string  `json:"url"`
	APIKey              *string `json:"api_key,omitempty"`
	PollIntervalSeconds int     `json:"poll_interval_seconds"`
	Enabled             bool    `json:"enabled"`
}

type MetricSourceParams struct {
	Name                string `json:"name"`
	URL                 string `json:"url"`
	APIKey              string `json:"api_key,omitempty"`
	PollIntervalSeconds int    `json:"poll_interval_seconds,omitempty"`
	Enabled             bool   `json:"enabled"`
}

type ProviderModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Provider struct {
	ID     string                   `json:"id"`
	Name   string                   `json:"name"`
	Models map[string]ProviderModel `json:"models"`
}

type ProvidersResponse struct {
	Providers []Provider        `json:"providers"`
	Default   map[string]string `json:"default"`
}

type CreateStationParams struct {
	Name       string `json:"name"`
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
	Directory  string `json:"directory,omitempty"`
}

// StationUpdate is a partial PATCH; nil fields are left out of the body.
// Alias points at "" to clear it.
type StationUpdate struct {
	Name          *string   `json:"name,omitempty"`
	Alias         *string   `json:"alias,omitempty"`
	Color         *string   `json:"color,omitempty"`
	TopBarActions *[]string `json:"top_bar_actions,omitempty"`
}

type MullvadOutput struct {
	Output string `json:"output"`
}

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) do(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, e.Error)
		}
		return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func esc(s string) string { return url.PathEscape(s) }

func (c *Client) Health() bool { return c.do("GET", "/health", nil, nil) == nil }

func (c *Client) ListStations() (out []Station, err error) {
	err = c.do("GET", "/stations", nil, &out)
	return
}

func (c *Client) GetStation(id uint) (out Station, err error) {
	err = c.do("GET", fmt.Sprintf("/stations/%d", id), nil, &out)
	return
}

func (c *Client) CreateStation(p CreateStationParams) (out Station, err error) {
	err = c.do("POST", "/stations", p, &out)
	return
}

func (c *Client) UpdateStation(id uint, u StationUpdate) (out Station, err error) {
	err = c.do("PATCH", fmt.Sprintf("/stations/%d", id), u, &out)
	return
}

func (c *Client) DeleteStation(id uint) error {
	return c.do("DELETE", fmt.Sprintf("/stations/%d", id), nil, nil)
}

func (c *Client) ResetStation(id uint) (out Station, err error) {
	err = c.do("POST", fmt.Sprintf("/stations/%d/reset", id), nil, &out)
	return
}

func (c *Client) CompactStation(id uint) error {
	return c.do("POST", fmt.Sprintf("/stations/%d/compact", id), nil, nil)
}

func (c *Client) AbortStation(id uint) error {
	return c.do("POST", fmt.Sprintf("/stations/%d/abort", id), nil, nil)
}

func (c *Client) Usage(id uint) (out TokenUsage, err error) {
	err = c.do("GET", fmt.Sprintf("/stations/%d/usage", id), nil, &out)
	return
}

// History returns raw opencode events, same shape as the WebSocket frames.
func (c *Client) History(id uint) (out []json.RawMessage, err error) {
	err = c.do("GET", fmt.Sprintf("/stations/%d/history", id), nil, &out)
	return
}

func (c *Client) DataPoints(id uint) (out []DataPoint, err error) {
	err = c.do("GET", fmt.Sprintf("/stations/%d/datapoints", id), nil, &out)
	return
}

func (c *Client) HideDataPoint(id uint, key string) error {
	return c.do("POST", fmt.Sprintf("/stations/%d/datapoints/%s/hide", id, esc(key)), nil, nil)
}

func (c *Client) ReorderDataPoints(id uint, order []string) error {
	return c.do("PUT", fmt.Sprintf("/stations/%d/datapoints/reorder", id), map[string][]string{"order": order}, nil)
}

func (c *Client) UpdateDataPointSettings(id uint, key string, s DataPointSettings) error {
	return c.do("PUT", fmt.Sprintf("/stations/%d/datapoints/%s/settings", id, esc(key)), s, nil)
}

func (c *Client) Providers() (out ProvidersResponse, err error) {
	err = c.do("GET", "/providers", nil, &out)
	return
}

func (c *Client) ListMetricSources() (out []MetricSource, err error) {
	err = c.do("GET", "/metric-sources", nil, &out)
	return
}

func (c *Client) CreateMetricSource(p MetricSourceParams) (out MetricSource, err error) {
	err = c.do("POST", "/metric-sources", p, &out)
	return
}

func (c *Client) UpdateMetricSource(id uint, p MetricSourceParams) error {
	return c.do("PUT", fmt.Sprintf("/metric-sources/%d", id), p, nil)
}

func (c *Client) DeleteMetricSource(id uint) error {
	return c.do("DELETE", fmt.Sprintf("/metric-sources/%d", id), nil, nil)
}

func (c *Client) MullvadStatus() (out MullvadOutput, err error) {
	err = c.do("GET", "/mullvad/status", nil, &out)
	return
}

func (c *Client) MullvadConnect() (out MullvadOutput, err error) {
	err = c.do("POST", "/mullvad/connect", nil, &out)
	return
}

func (c *Client) MullvadDisconnect() (out MullvadOutput, err error) {
	err = c.do("POST", "/mullvad/disconnect", nil, &out)
	return
}

func (c *Client) MullvadSetLocation(country, city, hostname string) (out MullvadOutput, err error) {
	err = c.do("POST", "/mullvad/location", map[string]string{"country": country, "city": city, "hostname": hostname}, &out)
	return
}

func (c *Client) MullvadSetLan(allow bool) (out MullvadOutput, err error) {
	err = c.do("POST", "/mullvad/lan", map[string]bool{"allow": allow}, &out)
	return
}

// WSURL builds a WebSocket URL for a server path. A handshake can't carry an
// Authorization header from every client, so the token goes in the query
// (see server/api/auth.go).
func (c *Client) WSURL(path string) string {
	u := strings.Replace(c.BaseURL, "http", "ws", 1) + path
	if c.Token != "" {
		u += "?token=" + url.QueryEscape(c.Token)
	}
	return u
}

func FormatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

func FormatValue(v float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	if decimals > 6 {
		decimals = 6
	}
	return fmt.Sprintf("%.*f", decimals, v)
}
