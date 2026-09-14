// Package opencode is a thin typed client over the subset of the opencode
// HTTP API (v2) that super-badger needs: session lifecycle, prompting, and
// provider/model listing. It intentionally does not wrap the full opencode
// surface (pty, mcp, lsp, vcs, tui control, ...) — super-badger only proxies
// what a Station needs.
//
// Deliberately NOT supported: registering a provider at runtime via
// PATCH /config. Confirmed live against a real opencode server (1.18.30)
// that this never actually wires into prompt-time model resolution — session
// creation succeeds, but every prompt fails with ProviderModelNotFoundError,
// regardless of directory scoping or call order. Stations must reference a
// provider/model already declared in opencode's own config file.
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	baseURL string
	// password is sent as HTTP Basic auth (any username — confirmed live
	// that opencode only checks the password) when opencode was launched
	// with OPENCODE_SERVER_PASSWORD set. Empty means opencode is unsecured.
	password string
	http     *http.Client
	// longHTTP has no fixed timeout, for calls that legitimately run long
	// (a model reply, or a held-open SSE stream) — bounded only by the
	// caller's context instead.
	longHTTP *http.Client
}

func NewClient(baseURL, password string) *Client {
	return &Client{
		baseURL:  baseURL,
		password: password,
		http:     &http.Client{Timeout: 60 * time.Second},
		longHTTP: &http.Client{},
	}
}

type Session struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Agent string `json:"agent,omitempty"`
}

// TokenUsage reports how much of the model's context window is currently
// occupied — Input/Output/Reasoning/CacheRead/CacheWrite come from the most
// recent assistant message's own usage snapshot, NOT the session-wide
// `tokens` field GET /session/{id} reports. That field is a running SUM
// across every message ever sent in the session (confirmed live), so it
// only ever grows — including right after a compaction, even though
// compaction's entire point is to shrink what's actually in context going
// forward. Each individual message's own `tokens` instead reflects the real
// context size for that specific request, which is what actually drops once
// a compacted summary replaces the older turns on the next prompt — that's
// the number worth showing here. Cost is the one figure kept cumulative,
// since total spend is meant to keep growing.
type TokenUsage struct {
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Reasoning  int     `json:"reasoning"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Cost       float64 `json:"cost"`
}

// GetSessionUsage reports the model's current context occupancy for a
// session — see TokenUsage's doc comment for why this reads the latest
// message's usage rather than the session's cumulative counter. Polling
// after a reply finishes is enough to keep it current; there's no dedicated
// event for it.
func (c *Client) GetSessionUsage(ctx context.Context, sessionID string) (TokenUsage, error) {
	var sess struct {
		Cost float64 `json:"cost"`
	}
	if err := c.do(ctx, "GET", fmt.Sprintf("/session/%s", sessionID), nil, &sess); err != nil {
		return TokenUsage{}, err
	}

	// A handful, most-recent-first: the very latest message can be a
	// just-sent user message (no tokens yet) or, rarely, an assistant
	// message that failed before any tokens were counted — walk back until
	// a real usage snapshot turns up.
	var messages []struct {
		Info struct {
			Role   string `json:"role"`
			Tokens struct {
				Input     int `json:"input"`
				Output    int `json:"output"`
				Reasoning int `json:"reasoning"`
				Cache     struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"info"`
	}
	path := fmt.Sprintf("/session/%s/message?order=desc&limit=5", sessionID)
	if err := c.do(ctx, "GET", path, nil, &messages); err != nil {
		return TokenUsage{}, err
	}

	for _, m := range messages {
		if m.Info.Role != "assistant" {
			continue
		}
		t := m.Info.Tokens
		if t.Input == 0 && t.Output == 0 {
			continue
		}
		return TokenUsage{
			Input:      t.Input,
			Output:     t.Output,
			Reasoning:  t.Reasoning,
			CacheRead:  t.Cache.Read,
			CacheWrite: t.Cache.Write,
			Cost:       sess.Cost,
		}, nil
	}
	return TokenUsage{Cost: sess.Cost}, nil
}

// Compact triggers the same conversation-summarization opencode's CLI runs
// via its /compact command — folds older turns into a summary so the
// session's context stays under the model's window instead of growing
// unbounded. Uses /session/{id}/summarize (v1), NOT the newer
// /api/session/{id}/compact (v2) endpoint the OpenAPI spec also lists —
// confirmed live that the v2 one 503s with "Session compact is not
// available yet" on this opencode build, while summarize works.
func (c *Client) Compact(ctx context.Context, sessionID, providerID, modelID string) error {
	body := map[string]any{
		"providerID": providerID,
		"modelID":    modelID,
	}
	return c.doWith(ctx, c.longHTTP, "POST", fmt.Sprintf("/session/%s/summarize", sessionID), body, nil)
}

// CreateSessionParams pins a new session to a specific provider/model and
// agent, and optionally a working directory — all in one call, confirmed
// live against a real opencode server rather than assumed from the spec.
type CreateSessionParams struct {
	ProviderID string
	ModelID    string
	Agent      string
	// Directory is the cwd opencode runs this session's agent in. Empty
	// leaves it to opencode's own default (its own project directory).
	Directory string
}

func (c *Client) CreateSession(ctx context.Context, p CreateSessionParams) (Session, error) {
	body := map[string]any{
		"agent": p.Agent,
		"model": map[string]any{
			"id":         p.ModelID,
			"providerID": p.ProviderID,
		},
	}

	path := "/session"
	if p.Directory != "" {
		path += "?directory=" + url.QueryEscape(p.Directory)
	}

	var sess Session
	if err := c.do(ctx, "POST", path, body, &sess); err != nil {
		return Session{}, err
	}
	return sess, nil
}

func (c *Client) AbortSession(ctx context.Context, sessionID string) error {
	return c.do(ctx, "POST", fmt.Sprintf("/session/%s/abort", sessionID), nil, nil)
}

func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	return c.do(ctx, "DELETE", fmt.Sprintf("/session/%s", sessionID), nil, nil)
}

// Prompt sends a text prompt into an existing session and blocks until the
// full response is ready, returning the raw opencode response body (message
// + parts) as json.RawMessage. Used only as the no-streaming fallback (see
// station.Service.Prompt) — a real model reply can take well over a minute,
// so this uses a client with no fixed timeout, relying on ctx for
// cancellation instead.
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (json.RawMessage, error) {
	body := map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": text},
		},
	}
	var raw json.RawMessage
	if err := c.doWith(ctx, c.longHTTP, "POST", fmt.Sprintf("/session/%s/message", sessionID), body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// PromptAsync sends a text prompt into an existing session and returns
// immediately (opencode responds 204) — the reply streams in as events on
// the /event SSE stream (see StreamEvents) instead of blocking this call.
func (c *Client) PromptAsync(ctx context.Context, sessionID, text string) error {
	body := map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": text},
		},
	}
	return c.do(ctx, "POST", fmt.Sprintf("/session/%s/prompt_async", sessionID), body, nil)
}

// StreamEvents opens opencode's global SSE event stream (message updates,
// part updates, session updates, ...) and returns the raw response for the
// caller to copy through — see api.streamEvents, which proxies this
// verbatim to the app rather than parsing/filtering it server-side (this is
// a local single-user dev tool; the app filters by its own session ID).
// The returned response's Body must be closed by the caller. No fixed
// timeout: the connection is meant to stay open indefinitely, bounded only
// by ctx.
func (c *Client) StreamEvents(ctx context.Context) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/event", nil)
	if err != nil {
		return nil, err
	}
	if c.password != "" {
		req.SetBasicAuth("opencode", c.password)
	}
	resp, err := c.longHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("opencode GET /event: status %d", resp.StatusCode)
	}
	return resp, nil
}

// ListPermissions returns every pending permission request across all
// sessions — see station.Service.PendingPermissions, which filters this down
// to one session so a freshly-connected station WebSocket can push whatever
// was already pending instead of only ever seeing ones asked after connect.
func (c *Client) ListPermissions(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, "GET", "/permission", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ReplyPermission answers a pending tool-permission request (opencode pauses
// the agent loop entirely until this is called — that's what "stuck on
// read" was: a permission.asked event nobody was listening for or able to
// answer). reply must be "once", "always", or "reject".
func (c *Client) ReplyPermission(ctx context.Context, requestID, reply string) error {
	body := map[string]any{"reply": reply}
	return c.do(ctx, "POST", fmt.Sprintf("/permission/%s/reply", requestID), body, nil)
}

// ListQuestions returns every pending question request across all sessions
// — opencode's AskUserQuestion-style tool, a distinct mechanism from
// permissions (see station.Service.PendingQuestions, which filters this down
// to one session the same way PendingPermissions does). Without a reply,
// opencode pauses the session's agent loop the same way an unanswered
// permission does.
func (c *Client) ListQuestions(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, "GET", "/question", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ReplyQuestion answers a pending question request. answers is one entry per
// question in the request (a QuestionRequest can carry more than one), each
// entry the selected option label(s) for that question.
func (c *Client) ReplyQuestion(ctx context.Context, requestID string, answers [][]string) error {
	body := map[string]any{"answers": answers}
	return c.do(ctx, "POST", fmt.Sprintf("/question/%s/reply", requestID), body, nil)
}

// RejectQuestion declines a pending question request outright (the
// question-mechanism equivalent of denying a permission).
func (c *Client) RejectQuestion(ctx context.Context, requestID string) error {
	return c.do(ctx, "POST", fmt.Sprintf("/question/%s/reject", requestID), nil, nil)
}

func (c *Client) ListProviders(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, "GET", "/config/providers", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) ListModels(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, "GET", "/api/model", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	return c.doWith(ctx, c.http, method, path, body, out)
}

func (c *Client) doWith(ctx context.Context, client *http.Client, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.password != "" {
		req.SetBasicAuth("opencode", c.password)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		return fmt.Errorf("opencode %s %s: status %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("opencode %s %s: decode response: %w", method, path, err)
		}
	}

	return nil
}
