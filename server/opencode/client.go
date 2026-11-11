// Package opencode is a thin typed client over the subset of the opencode
// HTTP API that super-badger needs. It targets opencode >= 2.2 only: every
// route lives under /api, responses are wrapped as {location?, data}, and
// HTTP Basic auth (user "opencode", password from OPENCODE_PASSWORD on the
// opencode process) is always required.
//
// Stations must reference a provider/model already declared in opencode's
// own config; super-badger never registers providers at runtime.
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Client struct {
	baseURL  string
	password string
	http     *http.Client
	// longHTTP has no fixed timeout, for calls that legitimately run long
	// (waiting on a reply, or a held-open SSE stream), bounded only by the
	// caller's context.
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

// APIError is a non-2xx answer from opencode.
type APIError struct {
	Method, Path string
	Status       int
	Tag          string // opencode's error _tag, e.g. "SessionNotFoundError"
	Body         string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("opencode %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// IsSessionNotFound reports whether err means the session no longer exists
// in opencode (deleted, its database reset, or an ID from before the
// opencode 2 cutover).
func IsSessionNotFound(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Tag == "SessionNotFoundError" || (ae.Status == http.StatusNotFound && ae.Tag == "")
}

type Session struct {
	ID    string  `json:"id"`
	Title string  `json:"title,omitempty"`
	Agent string  `json:"agent,omitempty"`
	Cost  float64 `json:"cost,omitempty"`
}

// CreateSessionParams pins a new session to a provider/model, agent and
// working directory.
type CreateSessionParams struct {
	ProviderID string
	ModelID    string
	Agent      string
	// Directory is the cwd opencode runs this session's agent in. Empty
	// leaves it to opencode's own default (its own project directory).
	Directory string
}

// denySubagents switches opencode's subagent tool off for every station
// session: the local models we run serve one request at a time, so a
// subagent call would queue behind (and deadlock on) the turn that spawned it.
var denySubagents = []map[string]string{{"action": "subagent", "resource": "*", "effect": "deny"}}

func (c *Client) CreateSession(ctx context.Context, p CreateSessionParams) (Session, error) {
	body := map[string]any{
		"agent":       p.Agent,
		"model":       map[string]any{"id": p.ModelID, "providerID": p.ProviderID},
		"permissions": denySubagents,
	}
	if p.Directory != "" {
		body["location"] = map[string]any{"directory": p.Directory}
	}
	var sess Session
	if err := c.do(ctx, "POST", "/api/session", body, &sess); err != nil {
		return Session{}, err
	}
	return sess, nil
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (Session, error) {
	var s Session
	err := c.do(ctx, "GET", "/api/session/"+sessionID, nil, &s)
	return s, err
}

func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	return c.do(ctx, "DELETE", "/api/session/"+sessionID, nil, nil)
}

// Delivery says when opencode hands a prompt to a busy session: "steer"
// at the next step boundary (between tool calls), "queue" after the turn.
type Delivery string

const (
	Steer Delivery = "steer"
	Queue Delivery = "queue"
)

// Prompt adds a user message to the session and returns at once with its
// message ID; the reply streams in on the event stream.
func (c *Client) Prompt(ctx context.Context, sessionID, text string, delivery Delivery) (string, error) {
	body := map[string]any{"text": text, "delivery": string(delivery)}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, "POST", "/api/session/"+sessionID+"/prompt", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// Synthetic adds text the model sees as context but that isn't a prompt
// from the user (used to seed a rolled-over session). With resume false it
// doesn't start a turn.
func (c *Client) Synthetic(ctx context.Context, sessionID, text, description string, resume bool) error {
	body := map[string]any{"text": text, "description": description, "resume": resume}
	return c.do(ctx, "POST", "/api/session/"+sessionID+"/synthetic", body, nil)
}

// Wait blocks until the session is idle.
func (c *Client) Wait(ctx context.Context, sessionID string) error {
	return c.doWith(ctx, c.longHTTP, "POST", "/api/experimental/session/"+sessionID+"/wait", nil, nil)
}

// Interrupt stops the session's running turn without starting anything
// still waiting in its inbox.
func (c *Client) Interrupt(ctx context.Context, sessionID string) error {
	return c.do(ctx, "POST", "/api/session/"+sessionID+"/interrupt?resume=false", nil, nil)
}

// Compact asks opencode to summarize the session's older turns. It returns
// at once with the compaction's message ID; progress arrives as
// session.compaction.{started,ended,failed} events.
func (c *Client) Compact(ctx context.Context, sessionID string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, "POST", "/api/session/"+sessionID+"/compact", map[string]any{}, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// Active returns the run state of every session opencode is running right
// now, keyed by session ID ("running", or e.g. "retry"). Idle sessions are
// absent.
func (c *Client) Active(ctx context.Context) (map[string]string, error) {
	var raw map[string]struct {
		Type string `json:"type"`
	}
	if err := c.do(ctx, "GET", "/api/session/active", nil, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for id, st := range raw {
		out[id] = st.Type
	}
	return out, nil
}

// Message is one entry of a session's message list. Only the fields
// super-badger reads are typed; content stays raw for the translator.
type Message struct {
	ID   string `json:"id"`
	Type string `json:"type"` // user|synthetic|assistant|compaction|idle|shell|...
	Time struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
	Text        string          `json:"text"`        // user, synthetic
	Description string          `json:"description"` // synthetic
	Content     json.RawMessage `json:"content"`     // assistant
	Finish      string          `json:"finish"`      // assistant
	Tokens      *StepTokens     `json:"tokens"`      // assistant, compaction
	Cost        float64         `json:"cost"`
	Status      string          `json:"status"`  // compaction: running|completed|failed
	Reason      string          `json:"reason"`  // compaction: auto|manual
	Summary     string          `json:"summary"` // compaction (completed)
	Outcome     string          `json:"outcome"` // idle
	Error       *StructuredErr  `json:"error"`
	Model       *ModelRef       `json:"model"`
}

type ModelRef struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
}

// StepTokens is opencode's per-step token usage.
type StepTokens struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	Cache     struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	} `json:"cache"`
}

// StructuredErr is opencode's error shape on events and messages.
type StructuredErr struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Status  int    `json:"status,omitempty"`
}

// MessagePage is one page of a session's messages plus the cursor for the
// next page in the same direction.
type MessagePage struct {
	Messages []Message
	Next     string
}

// MessagesParams selects a page: the first page (newest first unless Asc)
// when Cursor is empty, else the page Cursor points at. Type filters by
// message type.
type MessagesParams struct {
	Limit  int
	Cursor string
	Type   string
	Asc    bool
}

func (c *Client) Messages(ctx context.Context, sessionID string, p MessagesParams) (MessagePage, error) {
	q := url.Values{}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Cursor != "" {
		q.Set("cursor", p.Cursor)
	} else if p.Asc {
		q.Set("order", "asc")
	} else {
		q.Set("order", "desc")
	}
	if p.Type != "" {
		q.Set("type", p.Type)
	}
	var env struct {
		Data   []Message `json:"data"`
		Cursor struct {
			Next string `json:"next"`
		} `json:"cursor"`
	}
	if err := c.doRaw(ctx, c.http, "GET", "/api/session/"+sessionID+"/message?"+q.Encode(), nil, &env); err != nil {
		return MessagePage{}, err
	}
	return MessagePage{Messages: env.Data, Next: env.Cursor.Next}, nil
}

// PermissionRequest is a pending tool-permission request; opencode pauses
// the session's turn until it is answered.
type PermissionRequest struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionID"`
	Action    string          `json:"action"`
	Resources []string        `json:"resources"`
	Save      []string        `json:"save"`
	Metadata  json.RawMessage `json:"metadata"`
}

func (c *Client) Permissions(ctx context.Context, sessionID string) ([]PermissionRequest, error) {
	var out []PermissionRequest
	err := c.do(ctx, "GET", "/api/session/"+sessionID+"/permission", nil, &out)
	return out, err
}

// ReplyPermission answers a pending permission request: decision is
// "once", "always" or "reject".
func (c *Client) ReplyPermission(ctx context.Context, sessionID, requestID, decision string) error {
	return c.do(ctx, "POST", "/api/session/"+sessionID+"/permission/"+requestID+"/reply", map[string]any{"decision": decision}, nil)
}

// Form is a pending question form (opencode's question tool). Each field is
// one question; the session's turn waits for a reply.
type Form struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionID"`
	Title     string      `json:"title"`
	Fields    []FormField `json:"fields"`
}

type FormField struct {
	Key         string       `json:"key"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Type        string       `json:"type"`
	Options     []FormOption `json:"options"`
	Custom      bool         `json:"custom"`
}

type FormOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

func (c *Client) Forms(ctx context.Context, sessionID string) ([]Form, error) {
	var out []Form
	err := c.do(ctx, "GET", "/api/session/"+sessionID+"/form", nil, &out)
	return out, err
}

// ReplyForm answers a pending form: answer maps each field key to its
// value (a string, or a list of strings for a multiple-choice field).
func (c *Client) ReplyForm(ctx context.Context, sessionID, formID string, answer map[string]any) error {
	return c.do(ctx, "POST", "/api/session/"+sessionID+"/form/"+formID+"/reply", map[string]any{"answer": answer}, nil)
}

// RejectForm declines a pending form outright.
func (c *Client) RejectForm(ctx context.Context, sessionID, formID string) error {
	return c.do(ctx, "DELETE", "/api/session/"+sessionID+"/form/"+formID, nil, nil)
}

// Provider is an entry of GET /api/provider.
type Provider struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Package  string `json:"package"`
	Settings struct {
		BaseURL string `json:"baseURL"`
		APIKey  string `json:"apiKey"`
	} `json:"settings"`
}

func (c *Client) Providers(ctx context.Context) ([]Provider, error) {
	var out []Provider
	err := c.do(ctx, "GET", "/api/provider", nil, &out)
	return out, err
}

// Model is an entry of GET /api/model. Limit is the one opencode actually
// compacts against, after the super-badger plugin's detection and overrides.
type Model struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Enabled    bool   `json:"enabled"`
	Limit      struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	var out []Model
	err := c.do(ctx, "GET", "/api/model", nil, &out)
	return out, err
}

// StreamEvents opens opencode's global SSE event stream. The caller must
// close the response body. No fixed timeout: bounded only by ctx.
func (c *Client) StreamEvents(ctx context.Context) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/event", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.SetBasicAuth("opencode", c.password)
	resp, err := c.longHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &APIError{Method: "GET", Path: "/api/event", Status: resp.StatusCode, Body: string(b)}
	}
	return resp, nil
}

// do unwraps opencode's {data} envelope into out.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	return c.doWith(ctx, c.http, method, path, body, out)
}

func (c *Client) doWith(ctx context.Context, client *http.Client, method, path string, body any, out any) error {
	if out == nil {
		return c.doRaw(ctx, client, method, path, body, nil)
	}
	var raw json.RawMessage
	if err := c.doRaw(ctx, client, method, path, body, &raw); err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	// A few routes (interrupt) answer without the envelope.
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && len(env.Data) > 0 {
		raw = env.Data
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("opencode %s %s: decode response: %w", method, path, err)
	}
	return nil
}

// doRaw decodes the whole response body into out, envelope included.
func (c *Client) doRaw(ctx context.Context, client *http.Client, method, path string, body any, out any) error {
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth("opencode", c.password)

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
		var tag struct {
			Tag string `json:"_tag"`
		}
		_ = json.Unmarshal(respBody, &tag)
		return &APIError{Method: method, Path: path, Status: resp.StatusCode, Tag: tag.Tag, Body: string(respBody)}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("opencode %s %s: decode response: %w", method, path, err)
		}
	}
	return nil
}
