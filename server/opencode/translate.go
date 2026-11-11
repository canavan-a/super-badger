package opencode

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Event is super-badger's own client event: what the station WebSocket and
// GET /stations/:id/history carry, so the app and TUI never parse opencode's
// event or message formats themselves. Opencode events are translated into
// these as they arrive (Translator), and stored messages are translated into
// the same events for history (FromMessages), so live and restored
// transcripts are built by the same client reducer.
//
// Events are flat JSON objects keyed by "type":
//
//	msg              {messageID, role: user|assistant, done, finish?, error?, time?}
//	part             {messageID, partID, kind, ...}  upsert of one part; kind is
//	                   text|reasoning   text (full) or delta (append)
//	                   tool             tool, status: pending|running|completed|error, input?, output?, error?
//	                   step             tokens (the step's usage)
//	                   compaction       status: running|completed|failed, reason, error?
//	                   notice           text
//	status           {status: busy|idle|retry, attempt?, at?, error?}
//	compaction       {phase: started|ended|failed, reason: auto|manual, error?}
//	usage            {tokens (last step: what the context holds now), cost (session total), contextLimit?}
//	error            {error, kind: overflow|compaction|provider|other}
//	title            {text}
//	permission.asked {id, permission, patterns, metadata}
//	permission.replied {id}
//	question.asked   {id, questions: [{key, question, header, options[{label,description}], multiple, custom}]}
//	question.replied / question.rejected {id}
//	resync           {} the server's opencode stream reconnected: rebuild from history
//
// The server also sends its own flat messages on the same socket: notice
// {text, clear_error?}, error {error}, queue {items}, session_reset,
// heartbeat.
type Event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID,omitempty"`
	MessageID string `json:"messageID,omitempty"`
	PartID    string `json:"partID,omitempty"`

	Role   string `json:"role,omitempty"`
	Done   bool   `json:"done,omitempty"`
	Finish string `json:"finish,omitempty"`
	Time   int64  `json:"time,omitempty"`

	Kind   string          `json:"kind,omitempty"`
	Text   string          `json:"text,omitempty"`
	Delta  string          `json:"delta,omitempty"`
	Tool   string          `json:"tool,omitempty"`
	Status string          `json:"status,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Output string          `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`

	Phase   string `json:"phase,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	At      int64  `json:"at,omitempty"`

	Tokens       *Tokens  `json:"tokens,omitempty"`
	Cost         *float64 `json:"cost,omitempty"`
	ContextLimit int      `json:"contextLimit,omitempty"`

	ID         string          `json:"id,omitempty"`
	Permission string          `json:"permission,omitempty"`
	Patterns   []string        `json:"patterns,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	Questions  []Question      `json:"questions,omitempty"`
}

// Tokens is one step's usage. Ctx is what the context window holds after
// the step (input + output + cache), the figure the context gauge shows.
type Tokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Reasoning  int `json:"reasoning"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Ctx        int `json:"ctx"`
}

func tokensFrom(t *StepTokens) *Tokens {
	if t == nil {
		return nil
	}
	return &Tokens{
		Input: t.Input, Output: t.Output, Reasoning: t.Reasoning,
		CacheRead: t.Cache.Read, CacheWrite: t.Cache.Write,
		Ctx: t.Input + t.Output + t.Cache.Read + t.Cache.Write,
	}
}

type Question struct {
	Key      string           `json:"key"`
	Question string           `json:"question"`
	Header   string           `json:"header"`
	Options  []QuestionOption `json:"options"`
	Multiple bool             `json:"multiple"`
	Custom   bool             `json:"custom"`
}

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Value       string `json:"value,omitempty"`
}

// QuestionsFromForm maps a form's fields to questions, one per field.
func QuestionsFromForm(f Form) []Question {
	out := make([]Question, 0, len(f.Fields))
	for _, fl := range f.Fields {
		q := Question{Key: fl.Key, Question: fl.Description, Header: fl.Title, Custom: fl.Custom, Multiple: fl.Type == "multiselect"}
		if q.Question == "" {
			q.Question = fl.Title
		}
		for _, o := range fl.Options {
			label := o.Label
			if label == "" {
				label = o.Value
			}
			q.Options = append(q.Options, QuestionOption{Label: label, Description: o.Description, Value: o.Value})
		}
		out = append(out, q)
	}
	return out
}

// PermissionEvent shapes a pending permission request as permission.asked.
func PermissionEvent(p PermissionRequest) Event {
	return Event{Type: "permission.asked", SessionID: p.SessionID, ID: p.ID, Permission: p.Action, Patterns: p.Resources, Metadata: p.Metadata}
}

// QuestionEvent shapes a pending form as question.asked.
func QuestionEvent(f Form) Event {
	return Event{Type: "question.asked", SessionID: f.SessionID, ID: f.ID, Questions: QuestionsFromForm(f)}
}

// Envelope is one opencode /api/event frame.
type Envelope struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Created int64           `json:"created"`
	Data    json.RawMessage `json:"data"`
}

// SessionID is the session an opencode event belongs to, or "".
func (e Envelope) SessionID() string {
	var d struct {
		SessionID string `json:"sessionID"`
		Form      struct {
			SessionID string `json:"sessionID"`
		} `json:"form"`
	}
	_ = json.Unmarshal(e.Data, &d)
	if d.SessionID != "" {
		return d.SessionID
	}
	return d.Form.SessionID
}

// Part IDs. opencode identifies streamed text/reasoning by (message,
// ordinal) and tools by call ID; stored messages list the same content in
// order, so history rebuilds the same IDs by counting.
func textPartID(messageID, kind string, ordinal int) string {
	return messageID + "/" + kind + "/" + strconv.Itoa(ordinal)
}
func stepPartID(messageID string) string { return messageID + "/step" }

// ErrorKind classifies an opencode error for clients and auto mode.
func ErrorKind(e *StructuredErr) string {
	if e == nil {
		return "other"
	}
	switch {
	case e.Type == "aborted":
		return "aborted"
	case e.Type == "compaction.failed":
		return "compaction"
	case strings.Contains(strings.ToLower(e.Type), "overflow"), strings.Contains(strings.ToLower(e.Type), "context"),
		IsOverflowText(e.Message):
		return "overflow"
	case strings.HasPrefix(e.Type, "provider."):
		return "provider"
	default:
		return "other"
	}
}

// overflowPhrases are how engines word "the request no longer fits the
// context window". opencode recognises these itself and compacts, so they
// only reach us when its recovery failed. llama.cpp: "request (110096
// tokens) exceeds the available context size (110080 tokens)"; Strata:
// "request exceeds the context window: prompt (N tokens) + max tokens (M)
// exceeds the context (C)" / "... leaves no room to answer". The rest cover
// OpenAI/Anthropic/OpenRouter-style backends and opencode's own wording.
var overflowPhrases = []string{
	"exceeds the available context size",
	"exceeds the context", // also "... context window"
	"leaves no room to answer",
	"contextoverflowerror",
	"context overflow",
	"context_length_exceeded",
	"maximum context length",
	"context length exceeded",
	"exceeds context window",
	"prompt is too long",
	"input is too long for",
	"too many tokens",
}

// IsOverflowText reports whether an error text describes a context-window
// overflow.
func IsOverflowText(s string) bool {
	s = strings.ToLower(s)
	for _, p := range overflowPhrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// Translator turns opencode events into super-badger events. It keeps a
// little per-session state: prompt texts between enqueue and delivery,
// tool parts (so every tool update is a complete part), and text still
// streaming (so a client that loads history mid-reply gets it). Not safe for
// concurrent use; the EventBroker serializes calls.
type Translator struct {
	sessions map[string]*sessionState
}

type sessionState struct {
	inbox     map[string]inboxItem // inbox ID -> prompt, until delivered
	tools     map[string]*Event    // call ID -> latest tool part
	streaming map[string]*Event    // part ID -> text/reasoning part still streaming
	lastStep  *Tokens
	cost      float64
	model     ModelRef
}

func NewTranslator() *Translator { return &Translator{sessions: map[string]*sessionState{}} }

func (t *Translator) state(sid string) *sessionState {
	s := t.sessions[sid]
	if s == nil {
		s = &sessionState{inbox: map[string]inboxItem{}, tools: map[string]*Event{}, streaming: map[string]*Event{}}
		t.sessions[sid] = s
	}
	return s
}

// Forget drops a session's state (deleted or replaced).
func (t *Translator) Forget(sid string) { delete(t.sessions, sid) }

// Streaming returns copies of sid's text/reasoning parts still streaming,
// with the text so far.
func (t *Translator) Streaming(sid string) []Event {
	s := t.sessions[sid]
	if s == nil {
		return nil
	}
	out := make([]Event, 0, len(s.streaming))
	for _, p := range s.streaming {
		out = append(out, *p)
	}
	return out
}

// Usage returns sid's latest usage (nil before any step finished), and the
// session's model as last seen on a step.
func (t *Translator) Usage(sid string) (*Event, ModelRef) {
	s := t.sessions[sid]
	if s == nil || s.lastStep == nil {
		if s != nil {
			return nil, s.model
		}
		return nil, ModelRef{}
	}
	cost := s.cost
	return &Event{Type: "usage", SessionID: sid, Tokens: s.lastStep, Cost: &cost}, s.model
}

type evData struct {
	SessionID          string          `json:"sessionID"`
	AssistantMessageID string          `json:"assistantMessageID"`
	InboxID            string          `json:"inboxID"`
	Ordinal            int             `json:"ordinal"`
	Delta              string          `json:"delta"`
	Text               string          `json:"text"`
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Input              json.RawMessage `json:"input"`
	Content            []contentText   `json:"content"`
	Finish             string          `json:"finish"`
	Tokens             *StepTokens     `json:"tokens"`
	Cost               *float64        `json:"cost"`
	Reason             string          `json:"reason"`
	Attempt            int             `json:"attempt"`
	At                 int64           `json:"at"`
	Title              string          `json:"title"`
	RequestID          string          `json:"requestID"`
	Model              *ModelRef       `json:"model"`
	Error              json.RawMessage `json:"error"`
	Item               struct {
		Type    string `json:"type"`
		Payload struct {
			Text        string `json:"text"`
			Description string `json:"description"`
		} `json:"payload"`
	} `json:"item"`
	Form *Form `json:"form"`
	// permission.asked
	Action    string          `json:"action"`
	Resources []string        `json:"resources"`
	Metadata  json.RawMessage `json:"metadata"`
}

type contentText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// structuredErr reads an error that may be {type,message} or a bare string.
func structuredErr(raw json.RawMessage) *StructuredErr {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var e StructuredErr
	if json.Unmarshal(raw, &e) == nil && (e.Message != "" || e.Type != "") {
		return &e
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return &StructuredErr{Message: s}
	}
	return &StructuredErr{Message: string(raw)}
}

func errText(e *StructuredErr) string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Type
}

func joinContent(cs []contentText) string {
	var b strings.Builder
	for _, c := range cs {
		if c.Type == "text" || c.Text != "" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// Translate maps one opencode event to zero or more super-badger events.
func (t *Translator) Translate(env Envelope) []Event {
	sid := env.SessionID()
	if sid == "" {
		return nil
	}
	var d evData
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return nil
	}
	s := t.state(sid)
	mid := d.AssistantMessageID
	ev := func(e Event) Event { e.SessionID = sid; return e }

	switch env.Type {
	case "session.inbox.enqueued":
		if d.Item.Type == "user" || d.Item.Type == "synthetic" {
			s.inbox[d.InboxID] = inboxItem{text: d.Item.Payload.Text, description: d.Item.Payload.Description, synthetic: d.Item.Type == "synthetic"}
		}
	case "session.inbox.delivered":
		item, ok := s.inbox[d.InboxID]
		if !ok {
			return nil
		}
		delete(s.inbox, d.InboxID)
		m := Message{ID: d.InboxID, Type: "user", Text: item.text, Description: item.description}
		if item.synthetic {
			m.Type = "synthetic"
		}
		m.Time.Created = env.Created
		return FromMessages(sid, []Message{m})
	case "session.inbox.removed", "session.inbox.cancelled":
		delete(s.inbox, d.InboxID)

	case "session.execution.started":
		return []Event{ev(Event{Type: "status", Status: "busy"})}
	case "session.execution.succeeded", "session.execution.interrupted":
		return []Event{ev(Event{Type: "status", Status: "idle"})}
	case "session.execution.failed":
		e := structuredErr(d.Error)
		out := []Event{ev(Event{Type: "status", Status: "idle"})}
		if k := ErrorKind(e); k != "aborted" {
			out = append(out, ev(Event{Type: "error", Error: errText(e), Kind: k}))
		}
		return out
	case "session.retry.scheduled":
		return []Event{ev(Event{Type: "status", Status: "retry", Attempt: d.Attempt, At: d.At, Error: errText(structuredErr(d.Error))})}

	case "session.step.started":
		if d.Model != nil {
			s.model = *d.Model
		}
		return []Event{ev(Event{Type: "msg", MessageID: mid, Role: "assistant", Time: env.Created})}
	case "session.step.ended":
		tok := tokensFrom(d.Tokens)
		s.lastStep = tok
		cost := s.cost
		return []Event{
			ev(Event{Type: "part", MessageID: mid, PartID: stepPartID(mid), Kind: "step", Tokens: tok}),
			ev(Event{Type: "msg", MessageID: mid, Role: "assistant", Done: true, Finish: d.Finish}),
			ev(Event{Type: "usage", Tokens: tok, Cost: &cost}),
		}
	case "session.step.failed":
		e := structuredErr(d.Error)
		finish := "error"
		if ErrorKind(e) == "aborted" {
			finish = "aborted"
		}
		// Streaming parts of a failed step stop here.
		for id, p := range s.streaming {
			if p.MessageID == mid {
				delete(s.streaming, id)
			}
		}
		return []Event{ev(Event{Type: "msg", MessageID: mid, Role: "assistant", Done: true, Finish: finish, Error: errText(e)})}
	case "session.usage.updated":
		if d.Cost != nil {
			s.cost = *d.Cost
		}
		if s.lastStep == nil {
			return nil
		}
		cost := s.cost
		return []Event{ev(Event{Type: "usage", Tokens: s.lastStep, Cost: &cost})}

	case "session.text.started", "session.reasoning.started":
		kind := strings.TrimSuffix(strings.TrimPrefix(env.Type, "session."), ".started")
		p := Event{Type: "part", SessionID: sid, MessageID: mid, PartID: textPartID(mid, kind, d.Ordinal), Kind: kind}
		s.streaming[p.PartID] = &p
		return []Event{p}
	case "session.text.delta", "session.reasoning.delta":
		kind := strings.TrimSuffix(strings.TrimPrefix(env.Type, "session."), ".delta")
		id := textPartID(mid, kind, d.Ordinal)
		if p := s.streaming[id]; p != nil {
			p.Text += d.Delta
		}
		return []Event{ev(Event{Type: "part", MessageID: mid, PartID: id, Kind: kind, Delta: d.Delta})}
	case "session.text.ended", "session.reasoning.ended":
		kind := strings.TrimSuffix(strings.TrimPrefix(env.Type, "session."), ".ended")
		id := textPartID(mid, kind, d.Ordinal)
		delete(s.streaming, id)
		return []Event{ev(Event{Type: "part", MessageID: mid, PartID: id, Kind: kind, Text: d.Text})}

	case "session.tool.input.started":
		p := &Event{Type: "part", SessionID: sid, MessageID: mid, PartID: d.ID, Kind: "tool", Tool: d.Name, Status: "pending"}
		s.tools[d.ID] = p
		return []Event{*p}
	case "session.tool.called", "session.tool.progress", "session.tool.success", "session.tool.failed":
		p := s.tools[d.ID]
		if p == nil {
			p = &Event{Type: "part", SessionID: sid, MessageID: mid, PartID: d.ID, Kind: "tool", Tool: d.Name}
			s.tools[d.ID] = p
		}
		if p.Tool == "" {
			p.Tool = "tool"
		}
		if len(d.Input) > 0 {
			p.Input = d.Input
		}
		switch env.Type {
		case "session.tool.called", "session.tool.progress":
			p.Status = "running"
		case "session.tool.success":
			p.Status = "completed"
			p.Output = joinContent(d.Content)
			delete(s.tools, d.ID)
		case "session.tool.failed":
			p.Status = "error"
			p.Error = errText(structuredErr(d.Error))
			if p.Error == "" {
				p.Error = joinContent(d.Content)
			}
			delete(s.tools, d.ID)
		}
		return []Event{*p}

	case "session.compaction.started":
		return []Event{ev(Event{Type: "compaction", Phase: "started", Reason: d.Reason})}
	case "session.compaction.ended":
		// The next step's usage reflects the compacted context; until then
		// the old figure overstates it.
		s.lastStep = nil
		return []Event{ev(Event{Type: "compaction", Phase: "ended", Reason: d.Reason})}
	case "session.compaction.failed":
		return []Event{ev(Event{Type: "compaction", Phase: "failed", Reason: d.Reason, Error: errText(structuredErr(d.Error))})}

	case "session.renamed":
		return []Event{ev(Event{Type: "title", Text: d.Title})}

	case "permission.asked":
		return []Event{ev(Event{Type: "permission.asked", ID: d.ID, Permission: d.Action, Patterns: d.Resources, Metadata: d.Metadata})}
	case "permission.replied":
		id := d.RequestID
		if id == "" {
			id = d.ID
		}
		return []Event{ev(Event{Type: "permission.replied", ID: id})}

	case "form.created":
		if d.Form == nil {
			return nil
		}
		return []Event{QuestionEvent(*d.Form)}
	case "form.replied":
		return []Event{ev(Event{Type: "question.replied", ID: d.ID})}
	case "form.rejected", "form.cancelled", "form.deleted", "form.dismissed":
		return []Event{ev(Event{Type: "question.rejected", ID: d.ID})}

	case "session.deleted":
		t.Forget(sid)
	}
	return nil
}

// FromMessages translates stored messages (oldest first) into the same
// events live translation produces, for history.
func FromMessages(sessionID string, msgs []Message) []Event {
	var out []Event
	add := func(e Event) { e.SessionID = sessionID; out = append(out, e) }
	for _, m := range msgs {
		switch m.Type {
		case "user":
			add(Event{Type: "msg", MessageID: m.ID, Role: "user", Done: true, Time: m.Time.Created})
			add(Event{Type: "part", MessageID: m.ID, PartID: textPartID(m.ID, "text", 0), Kind: "text", Text: m.Text})
		case "synthetic":
			label := m.Description
			if label == "" {
				label = "Context added"
			}
			add(Event{Type: "msg", MessageID: m.ID, Role: "user", Done: true, Time: m.Time.Created})
			add(Event{Type: "part", MessageID: m.ID, PartID: m.ID + "/notice", Kind: "notice", Text: label})
		case "assistant":
			add(Event{Type: "msg", MessageID: m.ID, Role: "assistant", Time: m.Time.Created})
			ord := map[string]int{}
			var content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				State json.RawMessage `json:"state"`
			}
			_ = json.Unmarshal(m.Content, &content)
			for _, c := range content {
				switch c.Type {
				case "text", "reasoning":
					add(Event{Type: "part", MessageID: m.ID, PartID: textPartID(m.ID, c.Type, ord[c.Type]), Kind: c.Type, Text: c.Text})
					ord[c.Type]++
				case "tool":
					var st struct {
						Status  string          `json:"status"`
						Input   json.RawMessage `json:"input"`
						Content []contentText   `json:"content"`
						Error   json.RawMessage `json:"error"`
					}
					_ = json.Unmarshal(c.State, &st)
					p := Event{Type: "part", MessageID: m.ID, PartID: c.ID, Kind: "tool", Tool: c.Name, Status: st.Status, Input: st.Input}
					if p.Tool == "" {
						p.Tool = "tool"
					}
					switch st.Status {
					case "completed":
						p.Output = joinContent(st.Content)
					case "error", "failed":
						p.Status = "error"
						p.Error = errText(structuredErr(st.Error))
					case "":
						p.Status = "pending"
					}
					add(p)
				}
			}
			done := m.Time.Completed != 0 || m.Error != nil
			if m.Tokens != nil && done {
				add(Event{Type: "part", MessageID: m.ID, PartID: stepPartID(m.ID), Kind: "step", Tokens: tokensFrom(m.Tokens)})
			}
			if done {
				finish := m.Finish
				if m.Error != nil {
					finish = "error"
					if ErrorKind(m.Error) == "aborted" {
						finish = "aborted"
					}
				}
				add(Event{Type: "msg", MessageID: m.ID, Role: "assistant", Done: true, Finish: finish, Error: errText(m.Error)})
			}
		case "compaction":
			add(Event{Type: "msg", MessageID: m.ID, Role: "assistant", Done: m.Status != "running", Time: m.Time.Created})
			add(Event{Type: "part", MessageID: m.ID, PartID: m.ID + "/compaction", Kind: "compaction", Status: m.Status, Reason: m.Reason, Error: errText(m.Error)})
		}
	}
	return out
}

// LastUsage is the usage a session's newest stored step reports, for a
// client that connects between steps: the newest assistant message with
// tokens, unless a compaction came after it.
func LastUsage(sessionID string, newestFirst []Message, cost float64) *Event {
	for _, m := range newestFirst {
		switch m.Type {
		case "compaction":
			if m.Status == "completed" {
				return nil
			}
		case "assistant":
			if m.Tokens != nil && (m.Tokens.Input > 0 || m.Tokens.Output > 0) {
				c := cost
				return &Event{Type: "usage", SessionID: sessionID, Tokens: tokensFrom(m.Tokens), Cost: &c}
			}
		}
	}
	return nil
}

type inboxItem struct {
	text, description string
	synthetic         bool
}
