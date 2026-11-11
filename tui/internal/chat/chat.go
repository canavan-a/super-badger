// Package chat builds a station's transcript from super-badger's own event
// schema (server/opencode/translate.go): flat JSON events keyed by "type",
// carried by the station WebSocket and GET /stations/:id/history alike, plus
// a few flat server messages (notice, error, queue, session_reset).
//
// State is mutated in place; the bubbletea model owns it and only touches it
// from Update, so no locking is needed.
package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"superbadger-tui/internal/api"
)

type PartKind string

const (
	KindText       PartKind = "text"
	KindReasoning  PartKind = "reasoning"
	KindTool       PartKind = "tool"
	KindStepFinish PartKind = "step-finish"
	KindFile       PartKind = "file"
	KindSubtask    PartKind = "subtask"
	KindNotice     PartKind = "notice"
)

type Part struct {
	Kind   PartKind
	ID     string
	Text   string // text/reasoning body; label for file/subtask/notice
	Tool   string
	Status string // tool: pending|running|completed|error; compaction: running|completed|failed
	Title  string
	Input  any
	Output string
	Error  string
	Tokens int // a step's input+output+reasoning
	// A step's breakdown: Ctx is what the context window holds after the
	// step (the same figure as the top bar's "ctx"), Reasoning the thinking
	// tokens.
	Ctx, Reasoning int
}

func (p Part) hasText() bool { return p.Kind == KindText || p.Kind == KindReasoning }

type Turn struct {
	MessageID string
	Role      string // user|assistant
	PartOrder []string
	Parts     map[string]*Part
	Done      bool
	Finish    string // stop|tool-calls|length|error|aborted|...
	Error     string // a failed step's error text
}

type PendingPermission struct {
	ID         string
	Permission string
	Patterns   []string
}

type QuestionOption struct {
	Label       string
	Description string
	Value       string
}

// Question is one question of a question.asked request.
type Question struct {
	Key      string
	Question string
	Header   string
	Options  []QuestionOption
	Multiple bool
	Custom   bool
}

// PendingQuestion is a question.asked request. Its flat fields are the
// question currently being answered; Answer steps through the rest.
type PendingQuestion struct {
	ID       string
	Key      string
	Question string
	Header   string
	Options  []QuestionOption
	Multiple bool
	Custom   bool

	questions []Question
	answers   [][]string
}

func (q *PendingQuestion) show(i int) {
	c := q.questions[i]
	q.Key, q.Question, q.Header, q.Options, q.Multiple, q.Custom = c.Key, c.Question, c.Header, c.Options, c.Multiple, c.Custom
}

// Count is how many questions the request asks; Index which one is showing.
func (q *PendingQuestion) Count() int { return len(q.questions) }
func (q *PendingQuestion) Index() int { return len(q.answers) }

// Answer records the answer to the question showing (option labels or free
// text) and moves on to the next. done reports that every question has been
// answered, with all of them (one array per question, in order).
func (q *PendingQuestion) Answer(a []string) (all [][]string, done bool) {
	if a == nil {
		a = []string{}
	}
	q.answers = append(q.answers, a)
	if len(q.answers) >= len(q.questions) {
		return q.answers, true
	}
	q.show(len(q.answers))
	return nil, false
}

type State struct {
	Turns             []*Turn
	turnIndex         map[string]int
	Busy              bool
	Error             string
	Notice            string
	PendingPermission *PendingPermission
	PendingQuestion   *PendingQuestion
	// Compacting: a compaction is running (compaction events, or a running
	// compaction part in history).
	Compacting bool
	// Usage feeds the context gauge: usage events and GET /stations/:id/usage.
	Usage api.TokenUsage
	Title string
	// fresh: every turn held arrived live since the transcript was last
	// emptied (on open, or a session reset), none left over from an older
	// load, so a history snapshot that doesn't have them yet must keep them
	// (see Rebuilt).
	fresh bool
}

func New() *State { return &State{turnIndex: map[string]int{}, fresh: true} }

// Event is one flat event (see the package doc).
type Event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	PartID    string `json:"partID"`

	Role   string `json:"role"`
	Done   bool   `json:"done"`
	Finish string `json:"finish"`
	Time   int64  `json:"time"`

	Kind   string          `json:"kind"`
	Text   string          `json:"text"`
	Delta  string          `json:"delta"`
	Tool   string          `json:"tool"`
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
	Output string          `json:"output"`
	Error  string          `json:"error"`

	Phase   string `json:"phase"`
	Reason  string `json:"reason"`
	Attempt int    `json:"attempt"`

	Tokens       *api.Tokens `json:"tokens"`
	Cost         *float64    `json:"cost"`
	ContextLimit int         `json:"contextLimit"`

	ID         string     `json:"id"`
	Permission string     `json:"permission"`
	Patterns   []string   `json:"patterns"`
	Questions  []question `json:"questions"`

	// ClearError marks a notice as the server handling the current error
	// itself (auto mode compacting after a context overflow).
	ClearError bool `json:"clear_error"`
}

type question struct {
	Key      string `json:"key"`
	Question string `json:"question"`
	Header   string `json:"header"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
		Value       string `json:"value"`
	} `json:"options"`
	Multiple bool `json:"multiple"`
	Custom   bool `json:"custom"`
}

func ParseEvent(b []byte) (Event, error) {
	var e Event
	err := json.Unmarshal(b, &e)
	return e, err
}

func (s *State) ensureTurn(id, role string) *Turn {
	if i, ok := s.turnIndex[id]; ok {
		return s.Turns[i]
	}
	t := &Turn{MessageID: id, Role: role, Parts: map[string]*Part{}}
	s.Turns = append(s.Turns, t)
	s.turnIndex[id] = len(s.Turns) - 1
	return t
}

func (s *State) Turn(id string) *Turn {
	if i, ok := s.turnIndex[id]; ok {
		return s.Turns[i]
	}
	return nil
}

func (t *Turn) upsert(p *Part) {
	if _, ok := t.Parts[p.ID]; !ok {
		t.PartOrder = append(t.PartOrder, p.ID)
	}
	t.Parts[p.ID] = p
}

// toolTitle picks a short label for a tool call from its input (the events
// carry no title): what it says it does, or the file, pattern or URL.
func toolTitle(in any) string {
	m, _ := in.(map[string]any)
	for _, k := range []string{"description", "filePath", "path", "pattern", "url", "query"} {
		if s, _ := m[k].(string); s != "" {
			return s
		}
	}
	return ""
}

// compactionNotice is a compaction part's transcript marker.
func compactionNotice(status, errText string) string {
	switch status {
	case "running":
		return "Compacting…"
	case "failed":
		if errText != "" {
			return "Compaction failed: " + errText
		}
		return "Compaction failed"
	}
	return "Conversation compacted"
}

func (s *State) applyPart(e Event) {
	if e.MessageID == "" || e.PartID == "" {
		return
	}
	t := s.ensureTurn(e.MessageID, "assistant")
	s.Notice = ""
	switch e.Kind {
	case "text", "reasoning":
		kind := PartKind(e.Kind)
		ex, ok := t.Parts[e.PartID]
		if e.Delta != "" {
			if ok && ex.hasText() {
				ex.Text += e.Delta
			} else {
				t.upsert(&Part{Kind: kind, ID: e.PartID, Text: e.Delta})
			}
			return
		}
		// History replay can resolve after live deltas grew this part past
		// the snapshot; skip a stale prefix rather than snap the text back.
		if ok && ex.hasText() && len(ex.Text) > len(e.Text) && strings.HasPrefix(ex.Text, e.Text) {
			return
		}
		t.upsert(&Part{Kind: kind, ID: e.PartID, Text: e.Text})
	case "tool":
		p := &Part{Kind: KindTool, ID: e.PartID, Tool: e.Tool, Status: e.Status, Output: e.Output, Error: e.Error}
		if len(e.Input) > 0 {
			_ = json.Unmarshal(e.Input, &p.Input)
		}
		p.Title = toolTitle(p.Input)
		if p.Tool == "" {
			p.Tool = "tool"
		}
		if p.Status == "" {
			p.Status = "pending"
		}
		t.upsert(p)
	case "step":
		p := &Part{Kind: KindStepFinish, ID: e.PartID}
		if tk := e.Tokens; tk != nil {
			p.Tokens = tk.Input + tk.Output + tk.Reasoning
			p.Ctx = tk.Ctx
			p.Reasoning = tk.Reasoning
		}
		t.upsert(p)
	case "compaction":
		t.upsert(&Part{Kind: KindNotice, ID: e.PartID, Status: e.Status, Text: compactionNotice(e.Status, e.Error)})
		s.Compacting = e.Status == "running"
	case "notice":
		t.upsert(&Part{Kind: KindNotice, ID: e.PartID, Text: e.Text})
	}
}

// Apply folds one event into the state.
func (s *State) Apply(e Event) {
	switch e.Type {
	case "msg":
		if e.MessageID == "" {
			return
		}
		t := s.ensureTurn(e.MessageID, e.Role)
		if e.Role != "" {
			t.Role = e.Role
		}
		// Never undone: a stale history page can carry an older, unfinished
		// copy of a turn the live stream already finished.
		if e.Done {
			t.Done = true
		}
		if e.Finish != "" {
			t.Finish = e.Finish
		}
		if e.Error != "" {
			t.Error = e.Error
		}

	case "part":
		s.applyPart(e)

	// The only source of busy: live, and also sent by the server on every
	// connect, so a reply that finished (or started) while this client
	// wasn't watching still shows correctly.
	case "status":
		switch e.Status {
		case "busy":
			s.Busy = true
		case "retry":
			s.Busy = true
			n := fmt.Sprintf("Retrying (attempt %d)", e.Attempt)
			if e.Error != "" {
				n += ": " + e.Error
			}
			s.Notice = n
		case "idle":
			s.Busy = false
		}

	case "compaction":
		switch e.Phase {
		case "started":
			s.Compacting = true
			s.Notice = "Compacting…"
		case "ended":
			s.Compacting = false
			s.Notice = "Conversation compacted"
			// The old figure overstates the compacted context until the
			// next step reports.
			s.Usage.Tokens = nil
		case "failed":
			// The server reports the failure itself (error/notice).
			s.Compacting = false
		}

	case "usage":
		if e.Tokens != nil {
			tk := *e.Tokens
			s.Usage.Tokens = &tk
		}
		if e.Cost != nil {
			s.Usage.Cost = *e.Cost
		}
		if e.ContextLimit > 0 {
			s.Usage.ContextLimit = e.ContextLimit
		}

	case "title":
		s.Title = e.Text

	case "permission.asked":
		if e.ID == "" {
			return
		}
		s.PendingPermission = &PendingPermission{ID: e.ID, Permission: e.Permission, Patterns: e.Patterns}

	case "permission.replied":
		if s.PendingPermission != nil && (e.ID == "" || e.ID == s.PendingPermission.ID) {
			s.PendingPermission = nil
		}

	case "question.asked":
		if e.ID == "" || len(e.Questions) == 0 {
			return
		}
		pq := &PendingQuestion{ID: e.ID}
		for _, q := range e.Questions {
			c := Question{Key: q.Key, Question: q.Question, Header: q.Header, Multiple: q.Multiple, Custom: q.Custom}
			for _, o := range q.Options {
				c.Options = append(c.Options, QuestionOption{Label: o.Label, Description: o.Description, Value: o.Value})
			}
			pq.questions = append(pq.questions, c)
		}
		pq.show(0)
		s.PendingQuestion = pq

	case "question.replied", "question.rejected":
		if s.PendingQuestion != nil && (e.ID == "" || e.ID == s.PendingQuestion.ID) {
			s.PendingQuestion = nil
		}

	// Both opencode errors ({error, kind}) and the server's own ({error}).
	// Busy is left to status events.
	case "error":
		s.Error = e.Error
		if s.Error == "" {
			s.Error = "unknown error"
		}

	case "notice":
		s.Notice = e.Text
		if e.ClearError {
			s.Error = ""
		}

	// The station moved to a new session: the old session's turns go. The
	// model (so its context limit) is unchanged. The UI then reloads the new
	// session's history.
	case "session_reset":
		notice, limit := s.Notice, s.Usage.ContextLimit
		*s = *New()
		s.Notice, s.Usage.ContextLimit = notice, limit
	}
}

// Rebuilt is a fresh state built from a newest history page, keeping what
// history doesn't carry (run state, prompts, banners, usage) from s, and
// what arrived live after the snapshot was taken:
//   - turns the snapshot doesn't have yet: those after the newest turn both
//     share, or all of them while s is fresh (nothing in it is left over
//     from an older load, so a turn missing from the snapshot is newer);
//   - in a turn both have: live text grown past the snapshot's prefix of the
//     same part, parts started after the snapshot, and a done it hadn't seen.
func (s *State) Rebuilt(events []json.RawMessage) *State {
	n := New()
	n.Busy, n.Error, n.Notice = s.Busy, s.Error, s.Notice
	n.PendingPermission, n.PendingQuestion = s.PendingPermission, s.PendingQuestion
	n.Usage, n.Title = s.Usage, s.Title
	n.Seed(events)
	snapshotEmpty := len(n.Turns) == 0

	for _, t := range n.Turns {
		old := s.Turn(t.MessageID)
		if old == nil {
			continue
		}
		for _, id := range old.PartOrder {
			live, snap := old.Parts[id], t.Parts[id]
			switch {
			case snap == nil:
				t.upsert(live)
			case live.hasText() && snap.hasText() && len(live.Text) > len(snap.Text) && strings.HasPrefix(live.Text, snap.Text):
				t.Parts[id] = live
			}
		}
		if old.Done {
			t.Done = true
			if t.Finish == "" {
				t.Finish = old.Finish
			}
		}
	}

	lastShared := -1
	for i, t := range s.Turns {
		if n.Turn(t.MessageID) != nil {
			lastShared = i
		}
	}
	var live []*Turn
	switch {
	case s.fresh:
		live = s.Turns
	case lastShared >= 0:
		live = s.Turns[lastShared+1:]
	}
	for _, t := range live {
		if n.Turn(t.MessageID) == nil {
			n.Turns = append(n.Turns, t)
			n.turnIndex[t.MessageID] = len(n.Turns) - 1
		}
	}
	// Still fresh until a snapshot has something in it to anchor on.
	n.fresh = s.fresh && snapshotEmpty
	return n
}

// AddLocalUserTurn appends a finished user turn directly, without waiting for
// the server's echo.
func (s *State) AddLocalUserTurn(id, text string) {
	t := &Turn{MessageID: id, Role: "user", PartOrder: []string{id}, Done: true,
		Parts: map[string]*Part{id: {Kind: KindText, ID: id, Text: text}}}
	s.Turns = append(s.Turns, t)
	s.turnIndex[id] = len(s.Turns) - 1
}

// Seed replays cached history events; every event is an idempotent upsert.
func (s *State) Seed(events []json.RawMessage) {
	for _, raw := range events {
		if e, err := ParseEvent(raw); err == nil {
			s.Apply(e)
		}
	}
}

// Prepend puts an older page of history above the turns already shown. Seed
// can't do this: Apply appends any turn it hasn't seen, which would land
// older messages below newer ones. Turns already present are skipped (pages
// are disjoint by message, so that's only ever a defensive no-op).
func (s *State) Prepend(events []json.RawMessage) {
	page := New()
	page.Seed(events)
	var older []*Turn
	for _, t := range page.Turns {
		if _, ok := s.turnIndex[t.MessageID]; !ok {
			older = append(older, t)
		}
	}
	if len(older) == 0 {
		return
	}
	s.Turns = append(older, s.Turns...)
	s.turnIndex = make(map[string]int, len(s.Turns))
	for i, t := range s.Turns {
		s.turnIndex[t.MessageID] = i
	}
}

// Text concatenates a turn's text parts (used for plain rendering/tests).
func (t *Turn) Text() string {
	var b strings.Builder
	for _, id := range t.PartOrder {
		if p := t.Parts[id]; p != nil && p.Kind == KindText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// Marker reports whether a turn is only notices (a compaction marker, a
// synthetic "context carried over" turn): shown as a one-line marker rather
// than as a message.
func (t *Turn) Marker() bool {
	if len(t.PartOrder) == 0 {
		return false
	}
	for _, id := range t.PartOrder {
		if p := t.Parts[id]; p != nil && p.Kind != KindNotice {
			return false
		}
	}
	return true
}

func (p Part) String() string { return fmt.Sprintf("%s:%s", p.Kind, p.ID) }
