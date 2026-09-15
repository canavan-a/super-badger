// Package station implements Station lifecycle on top of the opencode API.
//
// The local LLMs behind a provider/model have no request concurrency: running
// two opencode sessions against the same backend at once corrupts state. So
// every Station operation that provisions a session first tears down any
// other session already pointed at that same provider/model, and the whole
// sequence is serialized per provider/model via backendLocks so concurrent
// API calls can't race two session creations against the same backend.
package station

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"

	"main/database"
	"main/opencode"
)

type Service struct {
	db       *gorm.DB
	oc       *opencode.Client
	broker   *opencode.EventBroker
	locksMu  sync.Mutex
	backendL map[string]*sync.Mutex
}

func NewService(db *gorm.DB, oc *opencode.Client, broker *opencode.EventBroker) *Service {
	return &Service{
		db:       db,
		oc:       oc,
		broker:   broker,
		backendL: make(map[string]*sync.Mutex),
	}
}

func (s *Service) backendLock(providerID, modelID string) *sync.Mutex {
	key := providerID + "|" + modelID
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	l, ok := s.backendL[key]
	if !ok {
		l = &sync.Mutex{}
		s.backendL[key] = l
	}
	return l
}

// agentBuild is the only agent Stations use. opencode's other "primary"
// agents (compaction/summary/title) are internal-use only, and "plan" was
// dropped as a user-facing choice — a Station is for driving real work, and
// exposing plan/build as a pick was more decision than the UI needed to ask.
const agentBuild = "build"

// CreateParams references a provider/model already declared in opencode's own
// config (GET /providers lists them) — see database.Station's doc comment
// for why a Station can't fully self-configure a model connection.
type CreateParams struct {
	Name       string `json:"name" binding:"required"`
	ProviderID string `json:"provider_id" binding:"required"`
	ModelID    string `json:"model_id" binding:"required"`
	Directory  string `json:"directory"`
}

func (s *Service) Create(ctx context.Context, p CreateParams) (database.Station, error) {
	st := database.Station{
		Name:       p.Name,
		ProviderID: p.ProviderID,
		ModelID:    p.ModelID,
		Agent:      agentBuild,
		Directory:  p.Directory,
		Status:     database.StatusIdle,
	}
	if err := database.CreateStation(s.db, &st); err != nil {
		return database.Station{}, err
	}

	if err := s.activate(ctx, &st); err != nil {
		return st, err
	}
	return st, nil
}

// Reset re-provisions the opencode session for an existing Station without
// deleting the Station row itself.
func (s *Service) Reset(ctx context.Context, id uint) (database.Station, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return database.Station{}, err
	}
	if err := s.activate(ctx, &st); err != nil {
		return st, err
	}
	return st, nil
}

// activate ensures st has a fresh, exclusive opencode session for its
// provider/model, aborting any other Station's session on the same backend
// first.
func (s *Service) activate(ctx context.Context, st *database.Station) error {
	lock := s.backendLock(st.ProviderID, st.ModelID)
	lock.Lock()
	defer lock.Unlock()

	if other, err := database.FindActiveStationForBackend(s.db, st.ProviderID, st.ModelID, st.ID); err != nil {
		return err
	} else if other != nil {
		_ = s.oc.AbortSession(ctx, other.OpencodeSessionID)
		_ = database.UpdateStationSession(s.db, other.ID, "", database.StatusIdle)
		s.broker.ClearHistory(other.OpencodeSessionID)
	}

	if st.OpencodeSessionID != "" {
		_ = s.oc.AbortSession(ctx, st.OpencodeSessionID)
		// This session is being replaced (manual reset or auto-recovery) —
		// its transcript belongs to a session that no longer exists and
		// must never be served as if it were still current.
		s.broker.ClearHistory(st.OpencodeSessionID)
	}

	sess, err := s.oc.CreateSession(ctx, opencode.CreateSessionParams{
		ProviderID: st.ProviderID,
		ModelID:    st.ModelID,
		Agent:      st.Agent,
		Directory:  st.Directory,
	})
	if err != nil {
		_ = database.UpdateStationSession(s.db, st.ID, "", database.StatusError)
		st.Status = database.StatusError
		return err
	}

	if err := database.UpdateStationSession(s.db, st.ID, sess.ID, database.StatusActive); err != nil {
		return err
	}
	st.OpencodeSessionID = sess.ID
	st.Status = database.StatusActive
	return nil
}

func (s *Service) List() ([]database.Station, error) {
	return database.ListStations(s.db)
}

func (s *Service) Get(id uint) (database.Station, error) {
	return database.GetStation(s.db, id)
}

func (s *Service) Prompt(ctx context.Context, id uint, text string) (json.RawMessage, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return nil, err
	}
	return s.oc.Prompt(ctx, st.OpencodeSessionID, text)
}

// PromptAsync kicks off a prompt without waiting for the reply — the caller
// is expected to be listening on this Station's WebSocket (see api.stationWS)
// for the streamed response instead.
func (s *Service) PromptAsync(ctx context.Context, id uint, text string) error {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return err
	}
	return s.oc.PromptAsync(ctx, st.OpencodeSessionID, text)
}

// ReplyPermission answers a pending tool-permission request from any
// station's session — see opencode.Client.ReplyPermission for why this
// exists at all (opencode pauses indefinitely otherwise).
func (s *Service) ReplyPermission(ctx context.Context, requestID, reply string) error {
	return s.oc.ReplyPermission(ctx, requestID, reply)
}

// PendingPermissions returns any permission requests opencode is currently
// blocked on for st's session, shaped as permission.asked events — so a
// freshly-connected station WebSocket (see api.stationWS) can push them
// immediately instead of only ever seeing ones asked *after* it connects,
// which would otherwise strand a client that reconnects (e.g. a page
// refresh) while one is already pending.
func (s *Service) PendingPermissions(ctx context.Context, id uint) ([]opencode.Event, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return nil, err
	}
	if st.OpencodeSessionID == "" {
		return nil, nil
	}

	raw, err := s.oc.ListPermissions(ctx)
	if err != nil {
		return nil, err
	}

	var all []struct {
		ID         string          `json:"id"`
		SessionID  string          `json:"sessionID"`
		Permission string          `json:"permission"`
		Patterns   json.RawMessage `json:"patterns"`
		Metadata   json.RawMessage `json:"metadata"`
		Always     json.RawMessage `json:"always"`
		Tool       json.RawMessage `json:"tool"`
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}

	var out []opencode.Event
	for _, p := range all {
		if p.SessionID != st.OpencodeSessionID {
			continue
		}
		props, err := json.Marshal(map[string]any{
			"id":         p.ID,
			"sessionID":  p.SessionID,
			"permission": p.Permission,
			"patterns":   p.Patterns,
			"metadata":   p.Metadata,
			"always":     p.Always,
			"tool":       p.Tool,
		})
		if err != nil {
			continue
		}
		out = append(out, opencode.Event{Type: "permission.asked", Properties: props})
	}
	return out, nil
}

// ReplyQuestion answers a pending question request from any station's
// session — opencode's AskUserQuestion-style tool, distinct from
// permissions (see opencode.Client.ReplyQuestion). Without this, opencode
// pauses the session's agent loop the same way an unanswered permission
// does — this is what "the question tool isn't passing back through" was.
func (s *Service) ReplyQuestion(ctx context.Context, requestID string, answers [][]string) error {
	return s.oc.ReplyQuestion(ctx, requestID, answers)
}

// RejectQuestion declines a pending question request outright.
func (s *Service) RejectQuestion(ctx context.Context, requestID string) error {
	return s.oc.RejectQuestion(ctx, requestID)
}

// PendingQuestions returns any question requests opencode is currently
// blocked on for st's session, shaped as question.asked events — same
// reconnect-strand fix as PendingPermissions, for the separate question
// mechanism.
func (s *Service) PendingQuestions(ctx context.Context, id uint) ([]opencode.Event, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return nil, err
	}
	if st.OpencodeSessionID == "" {
		return nil, nil
	}

	raw, err := s.oc.ListQuestions(ctx)
	if err != nil {
		return nil, err
	}

	var all []struct {
		ID        string          `json:"id"`
		SessionID string          `json:"sessionID"`
		Questions json.RawMessage `json:"questions"`
		Tool      json.RawMessage `json:"tool"`
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}

	var out []opencode.Event
	for _, q := range all {
		if q.SessionID != st.OpencodeSessionID {
			continue
		}
		props, err := json.Marshal(map[string]any{
			"id":        q.ID,
			"sessionID": q.SessionID,
			"questions": q.Questions,
			"tool":      q.Tool,
		})
		if err != nil {
			continue
		}
		out = append(out, opencode.Event{Type: "question.asked", Properties: props})
	}
	return out, nil
}

// Compact folds st's session's older turns into a summary (see
// opencode.Client.Compact) — the same thing the CLI's /compact command does,
// exposed here so long-running stations don't have to be reset (losing
// history) just to keep growing context under the model's window.
func (s *Service) Compact(ctx context.Context, id uint) error {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return err
	}
	if st.OpencodeSessionID == "" {
		return errors.New("station has no active session")
	}
	return s.oc.Compact(ctx, st.OpencodeSessionID, st.ProviderID, st.ModelID)
}

// Abort cancels st's session's in-flight turn (see opencode.Client.AbortSession)
// — the same thing the CLI's Escape/Ctrl-C does mid-response, exposed here so
// a long-running or stuck reply can be stopped from the app instead of the
// only options being waiting it out or Reset (which also wipes history).
func (s *Service) Abort(ctx context.Context, id uint) error {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return err
	}
	if st.OpencodeSessionID == "" {
		return errors.New("station has no active session")
	}
	return s.oc.AbortSession(ctx, st.OpencodeSessionID)
}

// TokenUsage reports st's session's current token/cost accounting (see
// opencode.Client.GetSessionUsage) — the same figures the CLI's status line
// shows, refreshed by opencode after every completed turn.
func (s *Service) TokenUsage(ctx context.Context, id uint) (opencode.TokenUsage, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return opencode.TokenUsage{}, err
	}
	if st.OpencodeSessionID == "" {
		return opencode.TokenUsage{}, nil
	}
	return s.oc.GetSessionUsage(ctx, st.OpencodeSessionID)
}

func (s *Service) Delete(ctx context.Context, id uint) error {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return err
	}
	if st.OpencodeSessionID != "" {
		_ = s.oc.AbortSession(ctx, st.OpencodeSessionID)
		_ = s.oc.DeleteSession(ctx, st.OpencodeSessionID)
		s.broker.ClearHistory(st.OpencodeSessionID)
	}
	return database.DeleteStation(s.db, id)
}

// History returns the small cached transcript for st's *current* session —
// empty if the session has none yet, and deliberately empty (not stale data)
// if the session was ever reset, since history is cleared together with the
// old session in activate().
func (s *Service) History(id uint) ([]opencode.Event, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return nil, err
	}
	if st.OpencodeSessionID == "" {
		return nil, nil
	}
	return s.broker.History(st.OpencodeSessionID), nil
}

// reachabilityTimeout bounds a single "is the model actually up" check so a
// dead backend can't stall a station list/detail fetch — st.Status only
// reflects whether a session was *successfully created*, never whether the
// model behind it is still answering right now, which is exactly what a
// user watching a stopped local model would otherwise be misled by. Kept
// short: checks for different stations run in parallel (see ReachableAll),
// but the whole list request still waits for the *slowest* one, so a dead
// model must not be allowed to make the entire list feel like it hung —
// confirmed live that 3s was clearly noticeable as "the list isn't loading."
// A live local model normally answers in well under 100ms.
const reachabilityTimeout = 800 * time.Millisecond

type providerEndpoint struct {
	BaseURL string
	APIKey  string
}

// providerEndpoints fetches opencode's provider list once and returns each
// provider's connection info keyed by ID, so checking N stations' liveness
// costs one opencode round trip, not N.
func (s *Service) providerEndpoints(ctx context.Context) (map[string]providerEndpoint, error) {
	raw, err := s.oc.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Providers []struct {
			ID      string `json:"id"`
			Options struct {
				BaseURL string `json:"baseURL"`
				APIKey  string `json:"apiKey"`
			} `json:"options"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make(map[string]providerEndpoint, len(resp.Providers))
	for _, p := range resp.Providers {
		out[p.ID] = providerEndpoint{BaseURL: p.Options.BaseURL, APIKey: p.Options.APIKey}
	}
	return out, nil
}

func reachable(ctx context.Context, ep providerEndpoint, ok bool) bool {
	if !ok {
		return false // provider no longer declared in opencode's config at all
	}
	if ep.BaseURL == "" {
		return true // hosted provider with no local endpoint to check
	}
	ctx, cancel := context.WithTimeout(ctx, reachabilityTimeout)
	defer cancel()
	return opencode.TestModelConnection(ctx, ep.BaseURL, ep.APIKey) == nil
}

// Reachable live-pings the model backend behind a single provider (via the
// baseURL opencode itself reports for it) — this is never persisted to
// st.Status; it's recomputed on every list/detail fetch so a model that's
// been stopped shows as unreachable immediately, not whatever was true when
// the session was created.
func (s *Service) Reachable(ctx context.Context, providerID string) bool {
	endpoints, err := s.providerEndpoints(ctx)
	if err != nil {
		return false
	}
	ep, ok := endpoints[providerID]
	return reachable(ctx, ep, ok)
}

// ReachableAll checks every given provider ID's liveness in parallel against
// one shared provider list fetch — the list endpoint's equivalent of
// Reachable, so an N-station list costs one opencode round trip plus N
// concurrent pings, not N round trips.
func (s *Service) ReachableAll(ctx context.Context, providerIDs []string) map[string]bool {
	out := make(map[string]bool, len(providerIDs))
	endpoints, err := s.providerEndpoints(ctx)
	if err != nil {
		for _, id := range providerIDs {
			out[id] = false
		}
		return out
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range providerIDs {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			ep, ok := endpoints[id]
			r := reachable(ctx, ep, ok)
			mu.Lock()
			out[id] = r
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// TestConnection validates a base URL/API key pair directly against the
// model endpoint, independent of opencode — e.g. to sanity-check a provider
// already declared in opencode's config (GET /providers reports its
// base URL) before pointing a Station at it.
func TestConnection(ctx context.Context, baseURL, apiKey string) error {
	return opencode.TestModelConnection(ctx, baseURL, apiKey)
}
