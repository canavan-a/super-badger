import {useCallback, useEffect, useRef, useState} from 'react';
import {AppState} from 'react-native';

import {getStationHistory} from './api';
import {
  addLocalUserTurn,
  applyEvent,
  ChatState,
  emptyChatState,
  prependHistory,
  rebuildFromHistory,
  reconcileLocalUserTurn,
} from './chat';
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
//
// 50ms used to be tried here, but each flush re-renders the streaming turn,
// which re-runs parseMarkdown (see markdown.ts) over the *entire*
// accumulated reply text, not just the new delta — cost that grows with the
// reply's length. On a long reply that added up to enough JS-thread work,
// 20x/sec, to visibly block navigation/touch handling and show up as
// flashing in the chat list. 150ms keeps streaming looking smooth while
// cutting that work by ~3x.
const FLUSH_INTERVAL_MS = 150;

// While busy, how long the socket can go without a single event before the
// busy flag is double-checked against the server's history. A long-running
// tool can legitimately be quiet this long, which is why this only checks
// rather than clearing busy outright.
const BUSY_STALL_MS = 45_000;

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
  // The single in-flight message's local turn id, set the moment pump() sends
  // it and cleared once opencode's own message.updated echo for it arrives
  // (see reconcileLocalUserTurn). Only ever one at a time — this station runs
  // one prompt at a time (see pump()'s own comment) — so there's never more
  // than one id to reconcile against.
  const pendingLocalUserID = useRef<string | null>(null);

  useEffect(() => {
    flushTimer.current = setInterval(() => {
      if (pending.current.length === 0) return;
      const batch = pending.current;
      pending.current = [];
      setState(prev => {
        let next = prev;
        for (const evt of batch) {
          if (pendingLocalUserID.current && evt.type === 'message.updated' && evt.properties?.info?.role === 'user') {
            next = reconcileLocalUserTurn(next, pendingLocalUserID.current, evt.properties.info.id);
            pendingLocalUserID.current = null;
          }
          next = applyEvent(next, evt);
        }
        return next;
      });
    }, FLUSH_INTERVAL_MS);
    return () => {
      if (flushTimer.current) clearInterval(flushTimer.current);
    };
  }, []);

  // Restores recent transcript (server-side ring buffer, see
  // server/opencode/events.go) when landing on a station — otherwise
  // navigating away and back always starts blank. Only the newest page is
  // fetched here; older pages come in via loadOlder as the user scrolls up,
  // since the whole cache can be several MB of tool output. If the session
  // was reset, the server's history for it is gone too (cleared together —
  // see station.Service.activate), so this naturally comes back empty rather
  // than showing stale, no-longer-real history.
  //
  // Runs on mount and again on every socket (re)connect — see
  // rebuildFromHistory for why reconnecting starts over from a fresh page
  // instead of keeping what was already loaded. `remount` bumps listKey so
  // the chat list also starts over (scroll position, paging window), the
  // same as a cold app start.
  const historyCursor = useRef('');
  const loadingOlderRef = useRef(false);
  const [hasMoreHistory, setHasMoreHistory] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [listKey, setListKey] = useState(0);
  const resyncSeq = useRef(0);
  const lastEventAt = useRef(Date.now());

  const resync = useCallback(
    (remount: boolean) => {
      const seq = ++resyncSeq.current;
      getStationHistory(stationId)
        .then(page => {
          if (seq !== resyncSeq.current) return;
          historyCursor.current = page.cursor;
          loadingOlderRef.current = false;
          setLoadingOlder(false);
          setHasMoreHistory(page.hasMore);
          setState(prev => {
            const {state: next, localEchoed} = rebuildFromHistory(prev, page.events, pendingLocalUserID.current);
            if (localEchoed) pendingLocalUserID.current = null;
            return next;
          });
          if (remount) setListKey(k => k + 1);
        })
        .catch(() => {
          // No history yet (or fetch failed) — keep what's shown; the next
          // reconnect or resume retries.
        });
    },
    [stationId],
  );

  useEffect(() => {
    historyCursor.current = '';
    setHasMoreHistory(false);
    resync(false);
    return () => {
      // Invalidates any fetch still in flight for the old station.
      // eslint-disable-next-line react-hooks/exhaustive-deps -- a counter, not a DOM ref
      resyncSeq.current++;
    };
  }, [resync]);

  // Safety net for a busy flag that outlived its reply: if the turn-finished
  // event was lost (socket half-dead, app suspended mid-reply), nothing else
  // would ever clear it and the send button stays a spinner. Only busy is
  // taken from the check — the transcript is left alone so a user reading
  // older history isn't disturbed.
  useEffect(() => {
    const timer = setInterval(() => {
      if (!busyRef.current || Date.now() - lastEventAt.current < BUSY_STALL_MS) return;
      lastEventAt.current = Date.now();
      if (pendingLocalUserID.current) {
        // Sent, but opencode never even echoed the message back — it didn't
        // arrive (sent into a socket that was already dead). Unblock the
        // composer rather than spin forever.
        pendingLocalUserID.current = null;
        setState(prev => ({
          ...prev,
          busy: false,
          error: 'The server never confirmed the last message — it may not have sent.',
        }));
        return;
      }
      getStationHistory(stationId)
        .then(page => {
          setState(prev => {
            const {state: checked} = rebuildFromHistory(prev, page.events, null);
            return checked.busy === prev.busy ? prev : {...prev, busy: checked.busy};
          });
        })
        .catch(() => {});
    }, 10_000);
    return () => clearInterval(timer);
  }, [stationId]);

  // Fetches the page of history just older than what's shown and puts it
  // above the current turns (see chat.ts's prependHistory). A no-op while a
  // fetch is already in flight, so a scroll handler can call it freely.
  const loadOlder = useCallback(() => {
    if (loadingOlderRef.current || !historyCursor.current) return;
    loadingOlderRef.current = true;
    setLoadingOlder(true);
    // A resync in between replaces the transcript and cursor this page was
    // requested against — splicing it in would duplicate/misorder turns.
    const seq = resyncSeq.current;
    getStationHistory(stationId, historyCursor.current)
      .then(page => {
        if (seq !== resyncSeq.current) return;
        historyCursor.current = page.cursor;
        setHasMoreHistory(page.hasMore);
        if (page.events.length > 0) setState(prev => prependHistory(prev, page.events));
      })
      .catch(() => {
        // Leave hasMore as-is so the next scroll to the top retries.
      })
      .finally(() => {
        loadingOlderRef.current = false;
        setLoadingOlder(false);
      });
  }, [stationId]);

  // Set by the socket effect below; tears down the current socket and opens a
  // fresh one right away (no 2s retry wait).
  const reconnectNowRef = useRef<() => void>(() => {});
  // pump (below) is defined after the socket effect but called from its
  // onopen.
  const pumpRef = useRef<() => void>(() => {});

  useEffect(() => {
    let cancelled = false;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let openedBefore = false;

    const connect = () => {
      if (cancelled) return;
      setStatus('connecting');
      const ws = new WebSocket(wsURL(stationId));
      wsRef.current = ws;

      ws.onopen = () => {
        if (cancelled || wsRef.current !== ws) return;
        setStatus('open');
        lastEventAt.current = Date.now();
        // Whatever happened while disconnected never arrived here — pick it
        // up from history. The first open just backs up the mount-time fetch
        // (nothing's been missed, so no need to restart the list).
        resync(openedBefore);
        openedBefore = true;
        pumpRef.current();
      };
      ws.onmessage = ev => {
        if (cancelled || wsRef.current !== ws) return;
        lastEventAt.current = Date.now();
        try {
          pending.current.push(JSON.parse(ev.data));
        } catch {
          // ignore malformed frames
        }
      };
      ws.onclose = () => {
        if (cancelled || wsRef.current !== ws) return;
        setStatus('closed');
        // Reconnect — a Station reset, opencode restart, or transient
        // network blip shouldn't strand the chat screen permanently.
        clearTimeout(retryTimer);
        retryTimer = setTimeout(connect, 2000);
      };
      ws.onerror = () => {
        ws.close();
      };
    };

    reconnectNowRef.current = () => {
      clearTimeout(retryTimer);
      const old = wsRef.current;
      wsRef.current = null; // detaches old's handlers (they check wsRef)
      old?.close();
      connect();
    };

    connect();

    return () => {
      cancelled = true;
      clearTimeout(retryTimer);
      wsRef.current?.close();
    };
  }, [stationId, resync]);

  // Coming back from the background, Android has often silently killed the
  // socket while it still reports OPEN — sends into it vanish (stuck send
  // spinner) and nothing new arrives (history "not loading until restart").
  // A fresh socket on every resume avoids trusting it; its onopen resyncs.
  useEffect(() => {
    let wasBackground = false;
    const sub = AppState.addEventListener('change', next => {
      if (next === 'background') wasBackground = true;
      if (next === 'active' && wasBackground) {
        wasBackground = false;
        reconnectNowRef.current();
      }
    });
    return () => sub.remove();
  }, []);

  // Sends the head of the outbox the moment nothing is in flight — called
  // both right after enqueueing (in case nothing was in flight already) and
  // whenever a reply finishes (see the busyRef effect below). Reading/mutating
  // busyRef instead of state.busy is what lets this run synchronously right
  // after enqueueing without waiting on a render.
  //
  // Only while the socket is actually open: sending into a connecting/closed
  // one silently drops the prompt, but busy would still go true with nothing
  // ever coming to clear it. The message waits in the outbox ("queued")
  // instead, and the socket's onopen pumps it.
  const pump = useCallback(() => {
    if (busyRef.current || outboxRef.current.length === 0) return;
    if (wsRef.current?.readyState !== WebSocket.OPEN) return;
    const [head, ...rest] = outboxRef.current;
    outboxRef.current = rest;
    busyRef.current = true;
    setOutboxVersion(v => v + 1);
    // Added as a real turn immediately rather than waiting on opencode's own
    // message.updated echo — see addLocalUserTurn's comment for why the
    // wait-for-echo approach could make a just-sent message vanish. Also
    // fixes the "queued" bubble never being seen at all when nothing was
    // busy: send() -> pump() dequeues synchronously in the same tick, so it
    // never rendered as queued before this either.
    const localID = `local-${head.id}`;
    pendingLocalUserID.current = localID;
    setState(s => ({...addLocalUserTurn(s, localID, head.text), busy: true, error: null}));
    lastEventAt.current = Date.now();
    wsRef.current.send(JSON.stringify({type: 'prompt', text: head.text}));
  }, []);
  pumpRef.current = pump;

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

  // After a force stop (kill): drop queued follow-ups and clear busy without
  // waiting on opencode's turn-finished event, which may never come if this
  // client never saw the run start.
  const forceIdle = useCallback(() => {
    outboxRef.current = [];
    busyRef.current = false;
    pendingLocalUserID.current = null;
    setOutboxVersion(v => v + 1);
    setState(prev => ({...prev, busy: false}));
  }, []);

  const reset = useCallback(() => {
    pending.current = [];
    outboxRef.current = [];
    busyRef.current = false;
    pendingLocalUserID.current = null;
    historyCursor.current = '';
    setHasMoreHistory(false);
    setOutboxVersion(v => v + 1);
    setState(emptyChatState);
  }, []);

  // After a Station reset (new opencode session), the open socket's
  // subscription is still keyed to the old session ID — closing it lets the
  // existing reconnect-on-close logic re-open against the station's
  // *current* session (the server resolves that fresh on every connect).
  const reconnect = useCallback(() => {
    reconnectNowRef.current();
  }, []);

  // eslint-disable-next-line react-hooks/exhaustive-deps -- outboxRef.current is intentionally read fresh each render, not tracked as a dependency
  const outbox = outboxRef.current;

  return {
    state,
    status,
    outbox,
    send,
    reset,
    forceIdle,
    reconnect,
    replyPermission,
    replyQuestion,
    rejectQuestion,
    dismissError,
    hasMoreHistory,
    loadingOlder,
    loadOlder,
    listKey,
  };
}
