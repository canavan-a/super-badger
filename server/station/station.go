// Package station implements Station lifecycle on top of the opencode API.
//
// Stations can run concurrent sessions against the same provider/model
// backend just fine — each Station gets its own independent opencode
// session, and activating one never touches another Station's session.
package station

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"main/database"
	"main/opencode"
)

type Service struct {
	db     *gorm.DB
	oc     *opencode.Client
	broker *opencode.EventBroker
	queue  *queueState
	auto   *autoState
}

func NewService(db *gorm.DB, oc *opencode.Client, broker *opencode.EventBroker) *Service {
	return &Service{
		db:     db,
		oc:     oc,
		broker: broker,
		queue:  newQueueState(),
		auto:   newAutoState(),
	}
}

// agentBuild is the only agent Stations use.
const agentBuild = "build"

// CreateParams references a provider/model already declared in opencode's own
// config (GET /providers lists them).
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
	// Queued follow-ups belong to the conversation being thrown away.
	if err := s.clearQueue(id); err != nil {
		return st, err
	}
	if err := s.activate(ctx, &st); err != nil {
		return st, err
	}
	return st, nil
}

// activate gives st a fresh opencode session for its provider/model,
// stopping the one it replaces (which stays in opencode's storage).
func (s *Service) activate(ctx context.Context, st *database.Station) error {
	if st.OpencodeSessionID != "" {
		_ = s.oc.Interrupt(ctx, st.OpencodeSessionID)
		s.broker.Forget(st.OpencodeSessionID)
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

// Update applies a partial set of field updates (name, alias, color) and
// returns the resulting Station.
func (s *Service) Update(id uint, updates map[string]any) (database.Station, error) {
	if err := database.UpdateStation(s.db, id, updates); err != nil {
		return database.Station{}, err
	}
	return database.GetStation(s.db, id)
}

// Prompt sends text and waits for the reply, returning the turn's events
// (the user message and everything after it). The no-streaming path; the
// app and TUI use the station WebSocket instead.
func (s *Service) Prompt(ctx context.Context, id uint, text string) ([]opencode.Event, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return nil, err
	}
	if st.OpencodeSessionID == "" {
		if err := s.activate(ctx, &st); err != nil {
			return nil, err
		}
	}
	msgID, err := s.oc.Prompt(ctx, st.OpencodeSessionID, text, opencode.Steer)
	if err != nil {
		return nil, err
	}
	if err := s.oc.Wait(ctx, st.OpencodeSessionID); err != nil {
		return nil, err
	}
	page, err := s.oc.Messages(ctx, st.OpencodeSessionID, opencode.MessagesParams{Limit: 50})
	if err != nil {
		return nil, err
	}
	// Newest first: keep everything down to (and including) our message.
	var turn []opencode.Message
	for _, m := range page.Messages {
		turn = append([]opencode.Message{m}, turn...)
		if m.ID == msgID {
			break
		}
	}
	return opencode.FromMessages(st.OpencodeSessionID, turn), nil
}

// ReplyPermission answers a pending tool-permission request in station id's
// session; opencode pauses the turn until it is answered.
func (s *Service) ReplyPermission(ctx context.Context, id uint, requestID, reply string) error {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return err
	}
	return s.oc.ReplyPermission(ctx, st.OpencodeSessionID, requestID, reply)
}

// PendingPermissions returns the permission requests st's session is
// blocked on, as permission.asked events — pushed to a freshly-connected
// station WebSocket so a request asked while no client was watching isn't
// stranded.
func (s *Service) PendingPermissions(ctx context.Context, id uint) ([]opencode.Event, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil || st.OpencodeSessionID == "" {
		return nil, err
	}
	reqs, err := s.oc.Permissions(ctx, st.OpencodeSessionID)
	if err != nil {
		return nil, err
	}
	out := make([]opencode.Event, 0, len(reqs))
	for _, r := range reqs {
		if r.SessionID == "" {
			r.SessionID = st.OpencodeSessionID
		}
		out = append(out, opencode.PermissionEvent(r))
	}
	return out, nil
}

// PendingQuestions returns the question forms st's session is blocked on,
// as question.asked events (same reconnect fix as PendingPermissions).
func (s *Service) PendingQuestions(ctx context.Context, id uint) ([]opencode.Event, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil || st.OpencodeSessionID == "" {
		return nil, err
	}
	forms, err := s.oc.Forms(ctx, st.OpencodeSessionID)
	if err != nil {
		return nil, err
	}
	out := make([]opencode.Event, 0, len(forms))
	for _, f := range forms {
		if f.SessionID == "" {
			f.SessionID = st.OpencodeSessionID
		}
		out = append(out, opencode.QuestionEvent(f))
	}
	return out, nil
}

// ReplyQuestion answers a pending question form. answers holds one entry
// per question, in order, each the chosen option label(s) or free text.
func (s *Service) ReplyQuestion(ctx context.Context, id uint, requestID string, answers [][]string) error {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return err
	}
	forms, err := s.oc.Forms(ctx, st.OpencodeSessionID)
	if err != nil {
		return err
	}
	for _, f := range forms {
		if f.ID == requestID {
			return s.oc.ReplyForm(ctx, st.OpencodeSessionID, f.ID, FormAnswer(f, answers))
		}
	}
	return fmt.Errorf("question %s is no longer pending", requestID)
}

// FormAnswer maps per-question answers onto a form's field keys. A chosen
// option label is sent as that option's value; anything else as typed.
func FormAnswer(f opencode.Form, answers [][]string) map[string]any {
	out := make(map[string]any, len(f.Fields))
	for i, fl := range f.Fields {
		var picked []string
		if i < len(answers) {
			for _, a := range answers[i] {
				v := a
				for _, o := range fl.Options {
					if o.Label == a && o.Value != "" {
						v = o.Value
					}
				}
				picked = append(picked, v)
			}
		}
		switch {
		case fl.Type == "multiselect":
			if picked == nil {
				picked = []string{}
			}
			out[fl.Key] = picked
		case len(picked) > 0:
			out[fl.Key] = picked[0]
		default:
			out[fl.Key] = ""
		}
	}
	return out
}

// RejectQuestion declines a pending question form outright.
func (s *Service) RejectQuestion(ctx context.Context, id uint, requestID string) error {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return err
	}
	return s.oc.RejectForm(ctx, st.OpencodeSessionID, requestID)
}

// SessionStatus reports whether st's session is running a turn right now
// ("busy", "retry" or "idle"), straight from opencode — sent to every
// freshly-connected station WebSocket so a client never has to guess.
func (s *Service) SessionStatus(ctx context.Context, id uint) (sessionID, status string, err error) {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return "", "", err
	}
	if st.OpencodeSessionID == "" {
		return "", "idle", nil
	}
	active, err := s.oc.Active(ctx)
	if err != nil {
		return "", "", err
	}
	return st.OpencodeSessionID, runStatus(active[st.OpencodeSessionID]), nil
}

func runStatus(active string) string {
	switch active {
	case "":
		return "idle"
	case "retry", "retrying":
		return "retry"
	default:
		return "busy"
	}
}

// compactWait caps how long a manual compaction is waited on.
const compactWait = 15 * time.Minute

// Compact folds st's session's older turns into a summary and waits for it
// to finish (or fail for good — the plugin redoes a failed summary once).
func (s *Service) Compact(ctx context.Context, id uint) error {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return err
	}
	if st.OpencodeSessionID == "" {
		return errors.New("station has no active session")
	}
	return s.compactAndWait(ctx, st.OpencodeSessionID)
}

// compactAndWait requests a compaction of sessionID and waits for its
// outcome.
func (s *Service) compactAndWait(ctx context.Context, sessionID string) error {
	events, unsubscribe := s.broker.Subscribe(sessionID)
	defer unsubscribe()
	if _, err := s.oc.Compact(ctx, sessionID); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, compactWait)
	defer cancel()
	return waitCompaction(ctx, events, healGrace)
}

// healGrace is how long a failed compaction is given to be redone (the
// plugin compacts again at once with its own summary) before it counts.
var healGrace = 3 * time.Minute

// waitCompaction waits for a compaction on events to end: nil when one
// ended, the error when one failed and nothing ended within grace after.
func waitCompaction(ctx context.Context, events <-chan opencode.Event, grace time.Duration) error {
	var failed error
	var graceC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if failed != nil {
				return failed
			}
			return fmt.Errorf("compaction did not finish: %w", ctx.Err())
		case <-graceC:
			return failed
		case e := <-events:
			switch {
			case e.Type == "compaction" && e.Phase == "ended":
				return nil
			case e.Type == "compaction" && e.Phase == "failed":
				failed = fmt.Errorf("compaction failed: %s", e.Error)
				if graceC == nil {
					graceC = time.After(grace)
				}
			}
		}
	}
}

// Abort stops st's session's running turn and drops anything still queued,
// so it isn't sent straight after.
func (s *Service) Abort(ctx context.Context, id uint) error {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return err
	}
	if st.OpencodeSessionID == "" {
		return errors.New("station has no active session")
	}
	if err := s.clearQueue(id); err != nil {
		return err
	}
	return s.oc.Interrupt(ctx, st.OpencodeSessionID)
}

// TokenUsage reports st's session's context usage: the last step's tokens
// (what the context holds now), the session's total cost, and the context
// window opencode compacts against.
func (s *Service) TokenUsage(ctx context.Context, id uint) (opencode.Event, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return opencode.Event{}, err
	}
	model := opencode.ModelRef{ID: st.ModelID, ProviderID: st.ProviderID}
	if st.OpencodeSessionID == "" {
		return opencode.Event{Type: "usage", ContextLimit: s.broker.ContextLimit(ctx, model)}, nil
	}
	u, err := s.broker.Usage(ctx, st.OpencodeSessionID)
	if opencode.IsSessionNotFound(err) {
		return opencode.Event{Type: "usage", ContextLimit: s.broker.ContextLimit(ctx, model)}, nil
	}
	if err != nil {
		return opencode.Event{}, err
	}
	if u.ContextLimit == 0 {
		u.ContextLimit = s.broker.ContextLimit(ctx, model)
	}
	return *u, nil
}

func (s *Service) Delete(ctx context.Context, id uint) error {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return err
	}
	if st.OpencodeSessionID != "" {
		_ = s.oc.Interrupt(ctx, st.OpencodeSessionID)
		_ = s.oc.DeleteSession(ctx, st.OpencodeSessionID)
		s.broker.Forget(st.OpencodeSessionID)
	}
	if err := s.clearQueue(id); err != nil {
		return err
	}
	return database.DeleteStation(s.db, id)
}

// HistoryPage returns one page of st's current session's transcript (see
// opencode.EventBroker.History). A session opencode no longer has yields an
// empty page.
func (s *Service) HistoryPage(ctx context.Context, id uint, before string, limit int) (events []opencode.Event, cursor string, hasMore bool, err error) {
	st, err := database.GetStation(s.db, id)
	if err != nil {
		return nil, "", false, err
	}
	if st.OpencodeSessionID == "" {
		return []opencode.Event{}, "", false, nil
	}
	events, cursor, hasMore, err = s.broker.History(ctx, st.OpencodeSessionID, before, limit)
	if opencode.IsSessionNotFound(err) {
		return []opencode.Event{}, "", false, nil
	}
	return events, cursor, hasMore, err
}

// reachabilityTimeout bounds a single "is the model actually up" check so a
// dead backend can't stall a station list/detail fetch. Checks run in
// parallel (ReachableAll) but the list waits for the slowest one.
const reachabilityTimeout = 800 * time.Millisecond

type providerEndpoint struct {
	BaseURL string
	APIKey  string
}

// providerEndpoints fetches opencode's provider list once and returns each
// provider's connection info keyed by ID.
func (s *Service) providerEndpoints(ctx context.Context) (map[string]providerEndpoint, error) {
	providers, err := s.oc.Providers(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]providerEndpoint, len(providers))
	for _, p := range providers {
		out[p.ID] = providerEndpoint{BaseURL: p.Settings.BaseURL, APIKey: p.Settings.APIKey}
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
	return opencode.ModelEndpointAlive(ctx, ep.BaseURL, ep.APIKey)
}

// Reachable live-pings the model backend behind a single provider (via the
// baseURL opencode reports for it); recomputed on every fetch, never stored.
func (s *Service) Reachable(ctx context.Context, providerID string) bool {
	endpoints, err := s.providerEndpoints(ctx)
	if err != nil {
		return false
	}
	ep, ok := endpoints[providerID]
	return reachable(ctx, ep, ok)
}

// ReachableAll checks every given provider ID's liveness in parallel against
// one shared provider list fetch.
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
// model endpoint, independent of opencode.
func TestConnection(ctx context.Context, baseURL, apiKey string) error {
	return opencode.TestModelConnection(ctx, baseURL, apiKey)
}

// EnsureSession gives station id a session when it has none (opencode was
// down when it was created, or the opencode 2 cutover cleared it).
func (s *Service) EnsureSession(ctx context.Context, id uint) (database.Station, error) {
	st, err := database.GetStation(s.db, id)
	if err != nil || st.OpencodeSessionID != "" {
		return st, err
	}
	l := s.queue.lock(id)
	l.Lock()
	defer l.Unlock()
	if st, err = database.GetStation(s.db, id); err != nil || st.OpencodeSessionID != "" {
		return st, err
	}
	err = s.activate(ctx, &st)
	return st, err
}
