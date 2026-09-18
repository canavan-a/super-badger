import {useCallback, useEffect, useRef, useState} from 'react';

import {getStationHistory} from './api';
import {applyEvent, ChatState, emptyChatState, seedFromHistory} from './chat';
import {settingsStore} from './settings';

function wsURL(stationId: number): string {
  const {serverUrl, authToken} = settingsStore.get();
  const base = serverUrl.replace(/^http/, 'ws') + `/stations/${stationId}/ws`;
  // A WebSocket handshake can't carry a custom Authorization header (unlike
  // the app's regular fetch calls — see src/api.ts's request()), so once the
  // server has auth enabled (see server/api/auth.go's RequireAuth) the token
  // has to travel as a query param instead.
  return authToken ? `${base}?token=${encodeURIComponent(authToken)}` : base;
}

export type ConnectionStatus = 'connecting' | 'open' | 'closed';

// A fast token stream can deliver 30-50+ message.part.delta events per
// second; applying each straight to React state would mean that many
// re-renders of the whole chat list per second. Instead, incoming events are
// buffered here and the reducer runs a whole batch per flush — one render
// per tick instead of one per token, independent of how fast the model
// streams.
const FLUSH_INTERVAL_MS = 50;

// One outgoing message the app is holding onto rather than handing straight
// to opencode — either because a reply is still in flight (this station only
// ever runs one prompt at a time — see station.Service.activate) or, briefly,
// between being queued and the socket actually accepting it. This is what
// lets the composer show "queued" the way opencode's own CLI does when you
// type ahead while it's still responding.
export interface QueuedMessage {
  id: string;
  text: string;
}

export function useStationChat(stationId: number) {
  const [state, setState] = useState<ChatState>(emptyChatState);
  const [status, setStatus] = useState<ConnectionStatus>('connecting');
  const wsRef = useRef<WebSocket | null>(null);
  const pending = useRef<any[]>([]);
  const flushTimer = useRef<ReturnType<typeof setInterval> | null>(null);
  // Mirrors state.busy for use inside pump() below without making pump
  // depend on (and thus be recreated by) every state change.
  const busyRef = useRef(false);
  // The outbox lives in a ref, not state, so pump() can synchronously check
  // and mutate it — a setState *updater* function doing that (the more
  // idiomatic-looking approach) gets re-invoked speculatively by React under
  // some conditions, which would double-send a queued message. outboxVersion
  // just forces a re-render whenever the ref's contents actually change.
  const outboxRef = useRef<QueuedMessage[]>([]);
  const [outboxVersion, setOutboxVersion] = useState(0);

  useEffect(() => {
    flushTimer.current = setInterval(() => {
      if (pending.current.length === 0) return;
      const batch = pending.current;
      pending.current = [];
      setState(prev => batch.reduce(applyEvent, prev));
    }, FLUSH_INTERVAL_MS);
    return () => {
      if (flushTimer.current) clearInterval(flushTimer.current);
    };
  }, []);

  // Restores a bit of recent transcript (server-side ring buffer, see
  // server/opencode/events.go) when landing on a station — otherwise
  // navigating away and back always starts blank. Order-independent versus
  // the live WS connection below (seedFromHistory/applyEvent are idempotent
  // upserts), so it's fine that this resolves whenever it resolves. If the
  // session was reset, the server's history for it is gone too (cleared
  // together — see station.Service.activate), so this naturally comes back
  // empty rather than showing stale, no-longer-real history.
  useEffect(() => {
    let cancelled = false;
    getStationHistory(stationId)
      .then(events => {
        if (cancelled || events.length === 0) return;
        setState(prev => seedFromHistory(prev, events));
      })
      .catch(() => {
        // No history yet (or fetch failed) — starting blank is correct here,
        // not an error worth surfacing.
      });
    return () => {
      cancelled = true;
    };
  }, [stationId]);

  useEffect(() => {
    let cancelled = false;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;

    const connect = () => {
      if (cancelled) return;
      setStatus('connecting');
      const ws = new WebSocket(wsURL(stationId));
      wsRef.current = ws;

      ws.onopen = () => {
        if (cancelled) return;
        setStatus('open');
      };
      ws.onmessage = ev => {
        if (cancelled) return;
        try {
          pending.current.push(JSON.parse(ev.data));
        } catch {
          // ignore malformed frames
        }
      };
      ws.onclose = () => {
        if (cancelled) return;
        setStatus('closed');
        // Reconnect — a Station reset, opencode restart, or transient
        // network blip shouldn't strand the chat screen permanently.
        retryTimer = setTimeout(connect, 2000);
      };
      ws.onerror = () => {
        ws.close();
      };
    };

    connect();

    return () => {
      cancelled = true;
      clearTimeout(retryTimer);
      wsRef.current?.close();
    };
  }, [stationId]);

  // Sends the head of the outbox the moment nothing is in flight — called
  // both right after enqueueing (in case nothing was in flight already) and
  // whenever a reply finishes (see the busyRef effect below). Reading/mutating
  // busyRef instead of state.busy is what lets this run synchronously right
  // after enqueueing without waiting on a render.
  const pump = useCallback(() => {
    if (busyRef.current || outboxRef.current.length === 0) return;
    const [head, ...rest] = outboxRef.current;
    outboxRef.current = rest;
    busyRef.current = true;
    setOutboxVersion(v => v + 1);
    setState(s => ({...s, busy: true, error: null}));
    wsRef.current?.send(JSON.stringify({type: 'prompt', text: head.text}));
  }, []);

  useEffect(() => {
    busyRef.current = state.busy;
    if (!state.busy) pump();
  }, [state.busy, pump]);

  // Enqueues rather than sending directly — this station's session can only
  // run one prompt at a time (see station.Service.activate), so a message
  // sent while busy needs somewhere to sit rather than racing the in-flight
  // one. pump() immediately tries to dispatch it in case nothing was busy.
  const send = useCallback(
    (text: string) => {
      const id = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      outboxRef.current = [...outboxRef.current, {id, text}];
      setOutboxVersion(v => v + 1);
      pump();
    },
    [pump],
  );

  // Answers a pending permission.asked request (see chat.ts's
  // PendingPermission) — without this, opencode leaves that session's agent
  // loop paused indefinitely waiting for a decision nothing could ever send.
  const replyPermission = useCallback((requestID: string, reply: 'once' | 'always' | 'reject') => {
    setState(prev => ({...prev, pendingPermission: null}));
    wsRef.current?.send(JSON.stringify({type: 'permission_reply', request_id: requestID, reply}));
  }, []);

  // Answers a pending question.asked request (see chat.ts's PendingQuestion)
  // — opencode's separate AskUserQuestion-style tool, same "pauses the agent
  // loop until answered" behavior as a permission but a distinct mechanism.
  const replyQuestion = useCallback((requestID: string, answers: string[][]) => {
    setState(prev => ({...prev, pendingQuestion: null}));
    wsRef.current?.send(JSON.stringify({type: 'question_reply', request_id: requestID, answers}));
  }, []);

  const rejectQuestion = useCallback((requestID: string) => {
    setState(prev => ({...prev, pendingQuestion: null}));
    wsRef.current?.send(JSON.stringify({type: 'question_reject', request_id: requestID}));
  }, []);

  // Lets the owner dismiss a session error banner rather than it sitting
  // there until the next prompt happens to clear it (pump() only resets
  // `error` right before sending a new one — see above).
  const dismissError = useCallback(() => {
    setState(prev => (prev.error ? {...prev, error: null} : prev));
  }, []);

  const reset = useCallback(() => {
    pending.current = [];
    outboxRef.current = [];
    busyRef.current = false;
    setOutboxVersion(v => v + 1);
    setState(emptyChatState);
  }, []);

  // After a Station reset (new opencode session), the open socket's
  // subscription is still keyed to the old session ID — closing it lets the
  // existing reconnect-on-close logic re-open against the station's
  // *current* session (the server resolves that fresh on every connect).
  const reconnect = useCallback(() => {
    wsRef.current?.close();
  }, []);

  // eslint-disable-next-line react-hooks/exhaustive-deps -- outboxRef.current is intentionally read fresh each render, not tracked as a dependency
  const outbox = outboxRef.current;

  return {
    state,
    status,
    outbox,
    send,
    reset,
    reconnect,
    replyPermission,
    replyQuestion,
    rejectQuestion,
    dismissError,
  };
}
