package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"main/database"
	"main/notify"
	"main/opencode"
	"main/station"
)

// No auth yet (see README) and this is a local dev tool, so any origin is
// accepted — matches the blanket CORS policy already set for the REST API.
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsClientMessage is what the app sends over a station's WebSocket:
// {"type":"prompt","text":"..."} to start a reply,
// {"type":"permission_reply","request_id":"...","reply":"once"|"always"|"reject"}
// to answer a pending tool-permission request, or
// {"type":"question_reply","request_id":"...","answers":[["label"]]} /
// {"type":"question_reject","request_id":"..."} to answer opencode's
// separate AskUserQuestion-style question mechanism (see question.asked
// events — without a reply, opencode pauses that session's agent loop the
// same way an unanswered permission does). Everything else on the wire
// flows server -> client (opencode.Event JSON, forwarded as-is from the
// shared EventBroker).
type wsClientMessage struct {
	Type      string     `json:"type"`
	Text      string     `json:"text"`
	RequestID string     `json:"request_id"`
	Reply     string     `json:"reply"`
	Answers   [][]string `json:"answers"`
}

// stationWS is one persistent, bidirectional connection per Station: the app
// sends {"type":"prompt","text":"..."} to start a reply, and receives every
// opencode event for that Station's session (message.part.updated as
// reasoning/text streams in, message.updated on completion) as it happens —
// this is what lets the chat UI show live streaming instead of blocking on
// one long HTTP call (see opencode.EventBroker for why one shared upstream
// connection to opencode backs every station's socket).
func stationWS(svc *station.Service, broker *opencode.EventBroker) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		st, err := svc.Get(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if st.OpencodeSessionID == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "station has no active session"})
			return
		}

		conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// gorilla/websocket connections aren't safe for concurrent writes,
		// so every write — forwarded opencode events and our own
		// error/notice messages alike — goes through this channel to a
		// single writer (the loop below), never conn.WriteJSON called
		// directly from more than one goroutine.
		writes := make(chan any, 16)

		// sessionChanged lets promptWithRecovery tell this loop to follow a
		// new session ID after a transparent reset (see below) — without
		// this, a station whose opencode session went stale (e.g. opencode
		// itself restarted, which drops all sessions from memory — confirmed
		// live) would stay subscribed to a dead session forever.
		sessionChanged := make(chan string, 1)
		done := make(chan struct{})

		// Reads run on their own goroutine so a slow/blocked write below
		// never stalls picking up the next incoming prompt.
		go func() {
			defer close(done)
			for {
				var msg wsClientMessage
				if err := conn.ReadJSON(&msg); err != nil {
					return
				}
				switch {
				case msg.Type == "prompt" && msg.Text != "":
					if err := promptWithRecovery(c.Request.Context(), svc, id, msg.Text, sessionChanged, writes); err != nil {
						select {
						case writes <- gin.H{"type": "error", "error": err.Error()}:
						case <-done:
						}
					}
				case msg.Type == "permission_reply" && msg.RequestID != "" && msg.Reply != "":
					if err := svc.ReplyPermission(c.Request.Context(), msg.RequestID, msg.Reply); err != nil {
						select {
						case writes <- gin.H{"type": "error", "error": err.Error()}:
						case <-done:
						}
					}
				case msg.Type == "question_reply" && msg.RequestID != "":
					if err := svc.ReplyQuestion(c.Request.Context(), msg.RequestID, msg.Answers); err != nil {
						select {
						case writes <- gin.H{"type": "error", "error": err.Error()}:
						case <-done:
						}
					}
				case msg.Type == "question_reject" && msg.RequestID != "":
					if err := svc.RejectQuestion(c.Request.Context(), msg.RequestID); err != nil {
						select {
						case writes <- gin.H{"type": "error", "error": err.Error()}:
						case <-done:
						}
					}
				}
			}
		}()

		sessionID := st.OpencodeSessionID
		events, unsubscribe := broker.Subscribe(sessionID)

		// A permission can already be pending before this socket ever
		// connects (e.g. asked while the app was closed, or the page was
		// refreshed with one outstanding) — the live subscription above only
		// sees events from this point forward, so without this a client
		// could be stuck waiting on a request it will never be shown.
		if pending, err := svc.PendingPermissions(c.Request.Context(), id); err == nil {
			for _, evt := range pending {
				select {
				case writes <- evt:
				default:
				}
			}
		}
		// Same reconnect-strand fix, for opencode's separate question
		// mechanism (see station.Service.PendingQuestions).
		if pending, err := svc.PendingQuestions(c.Request.Context(), id); err == nil {
			for _, evt := range pending {
				select {
				case writes <- evt:
				default:
				}
			}
		}

		for {
			select {
			case <-done:
				unsubscribe()
				return
			case newSessionID := <-sessionChanged:
				unsubscribe()
				sessionID = newSessionID
				events, unsubscribe = broker.Subscribe(sessionID)
			case evt, ok := <-events:
				if !ok {
					return
				}
				if err := conn.WriteJSON(evt); err != nil {
					unsubscribe()
					return
				}
			case w := <-writes:
				if err := conn.WriteJSON(w); err != nil {
					unsubscribe()
					return
				}
			}
		}
	}
}

// notificationsWS is a read-only, cross-station feed for exactly the two
// signals worth a background push notification: a station going idle (the
// agent finished, it's the user's turn again) and a station asking for a
// tool permission decision. Unlike stationWS (one connection per Station,
// bidirectional, full event stream) this is one connection for the whole
// app watching every Station at once — a mobile background service holds
// this open instead of opening N per-station sockets, so notification
// support costs one connection regardless of how many Stations exist.
func notificationsWS(svc *station.Service, broker *opencode.EventBroker, hub *notify.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				// The client never sends anything meaningful on this socket;
				// this loop exists purely to notice when it disconnects.
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()

		events, unsubscribe := broker.SubscribeAll()
		defer unsubscribe()

		// Separate feed for events that don't come from opencode at all
		// (e.g. a data point crossing a configured threshold — see
		// server/metrics.RunSource and notify.Hub).
		extra, unsubscribeExtra := hub.Subscribe()
		defer unsubscribeExtra()

		cache := newStationCache(svc)

		for {
			select {
			case <-done:
				return
			case msg, ok := <-extra:
				if !ok {
					return
				}
				if err := conn.WriteJSON(msg); err != nil {
					return
				}
			case evt, ok := <-events:
				if !ok {
					return
				}
				var msg gin.H
				switch evt.Type {
				case "session.idle":
					msg = gin.H{"type": "agent_idle"}
				case "permission.asked":
					msg = gin.H{"type": "permission_requested"}
				default:
					continue
				}
				st, found := cache.lookup(c.Request.Context(), evt.SessionID())
				if !found {
					continue // station deleted, or session belongs to nothing we track
				}
				msg["station_id"] = st.ID
				msg["station_name"] = st.Name
				if err := conn.WriteJSON(msg); err != nil {
					return
				}
			}
		}
	}
}

// stationCache resolves an opencode session ID to its owning Station without
// hitting the DB on every single event — refreshed lazily, bounded by
// cacheTTL, since this is a local single-user tool and staleness of a few
// seconds is harmless (a station created/reset in that window just waits for
// the next refresh to be resolvable).
// Not safe for concurrent use — fine here since exactly one goroutine (the
// select loop in notificationsWS) ever touches a given instance.
type stationCache struct {
	svc       *station.Service
	byID      map[string]database.Station
	refreshed time.Time
}

const cacheTTL = 15 * time.Second

func newStationCache(svc *station.Service) *stationCache {
	return &stationCache{svc: svc, byID: map[string]database.Station{}}
}

func (sc *stationCache) lookup(ctx context.Context, sessionID string) (database.Station, bool) {
	if sessionID == "" {
		return database.Station{}, false
	}
	if st, ok := sc.byID[sessionID]; ok {
		return st, true
	}
	if time.Since(sc.refreshed) < cacheTTL {
		return database.Station{}, false
	}
	sc.refresh()
	st, ok := sc.byID[sessionID]
	return st, ok
}

func (sc *stationCache) refresh() {
	sc.refreshed = time.Now()
	stations, err := sc.svc.List()
	if err != nil {
		return
	}
	sc.byID = make(map[string]database.Station, len(stations))
	for _, st := range stations {
		if st.OpencodeSessionID != "" {
			sc.byID[st.OpencodeSessionID] = st
		}
	}
}

// promptWithRecovery calls PromptAsync, and if opencode reports the session
// no longer exists (e.g. opencode itself restarted, which drops all sessions
// from memory — confirmed live), transparently resets the Station to a fresh
// session and retries once — notifying sessionChanged so stationWS's event
// subscription follows the new session ID rather than listening to a dead
// one forever, and telling the client what's happening (via writes) rather
// than just silently retrying behind its back.
func promptWithRecovery(ctx context.Context, svc *station.Service, id uint, text string, sessionChanged chan<- string, writes chan<- any) error {
	err := svc.PromptAsync(ctx, id, text)
	if err == nil || !strings.Contains(err.Error(), "Session not found") {
		return err
	}

	select {
	case writes <- gin.H{"type": "notice", "text": "Session expired — starting a new one…"}:
	default:
	}

	st, resetErr := svc.Reset(ctx, id)
	if resetErr != nil {
		return fmt.Errorf("session expired and reset failed: %w", resetErr)
	}
	select {
	case sessionChanged <- st.OpencodeSessionID:
	default:
	}
	// Tells the client to discard any transcript it's showing — it belongs
	// to the now-dead session, not this new one.
	select {
	case writes <- gin.H{"type": "session_reset"}:
	default:
	}
	return svc.PromptAsync(ctx, id, text)
}
