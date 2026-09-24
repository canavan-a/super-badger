// Package chat is a Go port of app/src/chat.ts: transcript state built from
// opencode's event stream (forwarded verbatim by the server's per-station
// WebSocket, plus a few flat server-synthesized messages).
//
// State is mutated in place; the bubbletea model owns it and only touches it
// from Update, so no locking is needed.
package chat

import (
	"encoding/json"
	"fmt"
	"strings"
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
	Status string // pending|running|completed|error
	Title  string
	Input  any
	Output string
	Error  string
	Tokens int
}

func (p Part) hasText() bool { return p.Kind == KindText || p.Kind == KindReasoning }

type Turn struct {
	MessageID string
	Role      string // user|assistant
	PartOrder []string
	Parts     map[string]*Part
	Done      bool
}

type PendingPermission struct {
	ID         string
	Permission string
	Patterns   []string
}

type QuestionOption struct {
	Label       string
	Description string
}

type PendingQuestion struct {
	ID       string
	Question string
	Header   string
	Options  []QuestionOption
	Multiple bool
	Custom   bool
}

type State struct {
	Turns             []*Turn
	turnIndex         map[string]int
	Busy              bool
	Error             string
	Notice            string
	PendingPermission *PendingPermission
	PendingQuestion   *PendingQuestion
}

func New() *State { return &State{turnIndex: map[string]int{}} }

// Event is either an opencode event ({id?, type, properties}) or a flat
// server message ({type:"error", error} / {type:"notice", text}).
type Event struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
	Error      string         `json:"error"`
	Text       string         `json:"text"`
}

func ParseEvent(b []byte) (Event, error) {
	var e Event
	err := json.Unmarshal(b, &e)
	return e, err
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func obj(m map[string]any, k string) map[string]any {
	o, _ := m[k].(map[string]any)
	return o
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

func partFromRaw(raw map[string]any) *Part {
	id := str(raw, "id")
	switch str(raw, "type") {
	case "text":
		return &Part{Kind: KindText, ID: id, Text: str(raw, "text")}
	case "reasoning":
		return &Part{Kind: KindReasoning, ID: id, Text: str(raw, "text")}
	case "tool":
		st := obj(raw, "state")
		p := &Part{Kind: KindTool, ID: id, Tool: str(raw, "tool"), Status: str(st, "status"),
			Title: str(st, "title"), Input: st["input"], Error: str(st, "error")}
		if p.Tool == "" {
			p.Tool = "tool"
		}
		if p.Status == "" {
			p.Status = "pending"
		}
		switch o := st["output"].(type) {
		case string:
			p.Output = o
		case nil:
		default:
			b, _ := json.Marshal(o)
			p.Output = string(b)
		}
		return p
	case "step-finish":
		p := &Part{Kind: KindStepFinish, ID: id}
		if f, ok := obj(raw, "tokens")["total"].(float64); ok {
			p.Tokens = int(f)
		}
		return p
	case "step-start":
		return nil
	case "file", "patch", "snapshot":
		label := str(raw, "filename")
		if label == "" {
			label = str(raw, "path")
		}
		if label == "" {
			label = str(raw, "type")
		}
		return &Part{Kind: KindFile, ID: id, Text: label}
	case "agent", "subtask":
		label := str(raw, "description")
		if label == "" {
			label = str(raw, "agent")
		}
		if label == "" {
			label = "subtask"
		}
		return &Part{Kind: KindSubtask, ID: id, Text: label}
	case "retry":
		return &Part{Kind: KindNotice, ID: id, Text: "Retrying…"}
	case "compaction":
		return &Part{Kind: KindNotice, ID: id, Text: "Conversation compacted"}
	default:
		return &Part{Kind: KindNotice, ID: id, Text: "[" + str(raw, "type") + "]"}
	}
}

// Apply folds one event into the state (chat.ts applyEvent).
func (s *State) Apply(e Event) {
	p := e.Properties
	switch e.Type {
	case "message.updated":
		info := obj(p, "info")
		if info == nil {
			return
		}
		t := s.ensureTurn(str(info, "id"), str(info, "role"))
		if obj(info, "time")["completed"] != nil {
			t.Done = true
			// A completed assistant message is as good a "reply finished"
			// signal as session.idle, which isn't reliably delivered.
			if str(info, "role") == "assistant" {
				s.Busy = false
			}
		}

	case "message.part.updated":
		raw := obj(p, "part")
		if raw == nil {
			return
		}
		part := partFromRaw(raw)
		if part == nil {
			return
		}
		msgID := str(raw, "messageID")
		t := s.ensureTurn(msgID, "assistant")
		// History replay can resolve after live deltas grew this part past
		// the snapshot; skip a stale prefix rather than snap the text back.
		if ex, ok := t.Parts[part.ID]; ok && ex.hasText() && part.hasText() &&
			len(ex.Text) > len(part.Text) && strings.HasPrefix(ex.Text, part.Text) {
			s.Notice = ""
			return
		}
		t.upsert(part)
		s.Notice = ""

	case "message.part.delta":
		msgID, partID := str(p, "messageID"), str(p, "partID")
		if msgID == "" || partID == "" || str(p, "field") != "text" {
			return
		}
		t := s.Turn(msgID)
		if t == nil {
			return
		}
		if ex, ok := t.Parts[partID]; ok && ex.hasText() {
			ex.Text += str(p, "delta")
		}

	case "session.idle":
		s.Busy = false

	case "permission.asked":
		id := str(p, "id")
		if id == "" {
			return
		}
		pp := &PendingPermission{ID: id, Permission: str(p, "permission")}
		if pats, ok := p["patterns"].([]any); ok {
			for _, x := range pats {
				if sx, ok := x.(string); ok {
					pp.Patterns = append(pp.Patterns, sx)
				}
			}
		}
		s.PendingPermission = pp

	case "permission.replied":
		s.PendingPermission = nil

	case "question.asked":
		id := str(p, "id")
		qs, _ := p["questions"].([]any)
		if id == "" || len(qs) == 0 {
			return
		}
		q, _ := qs[0].(map[string]any)
		pq := &PendingQuestion{ID: id, Question: str(q, "question"), Header: str(q, "header")}
		pq.Multiple, _ = q["multiple"].(bool)
		pq.Custom, _ = q["custom"].(bool)
		if opts, ok := q["options"].([]any); ok {
			for _, o := range opts {
				om, _ := o.(map[string]any)
				pq.Options = append(pq.Options, QuestionOption{str(om, "label"), str(om, "description")})
			}
		}
		s.PendingQuestion = pq

	case "question.replied", "question.rejected":
		s.PendingQuestion = nil

	case "session.error":
		msg := str(obj(p, "error"), "message")
		if msg == "" {
			msg = str(p, "message")
		}
		if msg == "" {
			msg = "session error"
		}
		s.Busy = false
		s.Error = msg

	// Server-synthesized (server/api/ws.go).
	case "error":
		s.Busy = false
		s.Error = e.Error
		if s.Error == "" {
			s.Error = "unknown error"
		}

	case "notice":
		s.Notice = e.Text

	case "session_reset":
		notice := s.Notice
		*s = *New()
		s.Notice = notice
	}
}

// AddLocalUserTurn shows the user's message immediately, without waiting for
// opencode's echo.
func (s *State) AddLocalUserTurn(id, text string) {
	t := &Turn{MessageID: id, Role: "user", PartOrder: []string{id}, Done: true,
		Parts: map[string]*Part{id: {Kind: KindText, ID: id, Text: text}}}
	s.Turns = append(s.Turns, t)
	s.turnIndex[id] = len(s.Turns) - 1
}

// ReconcileLocalUserTurn swaps an optimistic turn's key for opencode's real
// messageID once its own event arrives, so the live stream folds into the
// same turn rather than duplicating it.
func (s *State) ReconcileLocalUserTurn(localID, realID string) {
	i, ok := s.turnIndex[localID]
	if !ok || localID == realID {
		return
	}
	t := s.Turns[i]
	t.MessageID = realID
	t.Parts = map[string]*Part{}
	t.PartOrder = nil
	delete(s.turnIndex, localID)
	s.turnIndex[realID] = i
}

// Seed replays cached history events; every event is an idempotent upsert.
func (s *State) Seed(events []json.RawMessage) {
	for _, raw := range events {
		if e, err := ParseEvent(raw); err == nil {
			s.Apply(e)
		}
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

func (p Part) String() string { return fmt.Sprintf("%s:%s", p.Kind, p.ID) }
