import {useCallback, useEffect, useRef, useState} from 'react';
import {AppState} from 'react-native';

import {getStationHistory, getStationRunStatus} from './api';
import {applyEvent, ChatState, emptyChatState, prependHistory, rebuildFromHistory} from './chat';
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
// busy flag is double-checked against the server. A long-running tool can
// legitimately be quiet this long, which is why this only checks rather than
// clearing busy outright.
const BUSY_STALL_MS = 45_000;

// One message waiting to go to opencode. The queue itself lives on the
// server (see server/station/queue.go) — shared with every other app/TUI on
// this station, kept across app restarts, and sent by the server straight
// away, even mid-reply (opencode picks it up between tool calls).
// `local` ones were typed while the socket was down and haven't reached the
// server yet; they're handed over the moment it reconnects.
export interface QueuedMessage {
  id: string;
  text: string;
  local?: boolean;
}

export function useStationChat(stationId: number) {
  const [state, setState] = useState<ChatState>(emptyChatState);
  const [status, setStatus] = useState<ConnectionStatus>('connecting');
  const wsRef = useRef<WebSocket | null>(null);
  const pending = useRef<any[]>([]);
  const flushTimer = useRef<ReturnType<typeof setInterval> | null>(null);
  // Mirrors state.busy for the stall check below without making it depend
  // on (and thus be recreated by) every state change.
  const busyRef = useRef(false);
  busyRef.current = state.busy;
  // The server's queue, replaced wholesale by every {"type":"queue"}
  // snapshot it pushes.
  const [serverQueue, setServerQueue] = useState<QueuedMessage[]>([]);
  // Typed while disconnected — in a ref too so the socket's onopen can hand
  // them over without depending on render timing.
  const unsentRef = useRef<QueuedMessage[]>([]);
  const [unsent, setUnsent] = useState<QueuedMessage[]>([]);
  const setUnsentBoth = (next: QueuedMessage[]) => {
    unsentRef.current = next;
    setUnsent(next);
  };

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
          setState(prev => rebuildFromHistory(prev, page.events));
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
  // would ever clear it. Asks opencode directly, so a long quiet tool run
  // (still genuinely busy) is left alone.
  useEffect(() => {
    const timer = setInterval(() => {
      if (!busyRef.current || Date.now() - lastEventAt.current < BUSY_STALL_MS) return;
      lastEventAt.current = Date.now();
      getStationRunStatus(stationId)
        .then(({status: run}) => {
          if (run === 'idle') setState(prev => (prev.busy ? {...prev, busy: false} : prev));
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
        // Hand over anything typed while offline; the server's queue
        // snapshot that follows shows them as properly queued.
        for (const m of unsentRef.current) {
          ws.send(JSON.stringify({type: 'prompt', text: m.text}));
        }
        if (unsentRef.current.length > 0) setUnsentBoth([]);
      };
      ws.onmessage = ev => {
        if (cancelled || wsRef.current !== ws) return;
        lastEventAt.current = Date.now();
        let msg: any;
        try {
          msg = JSON.parse(ev.data);
        } catch {
          return; // ignore malformed frames
        }
        if (msg.type === 'queue') {
          // Applied right away rather than batched: it's a whole snapshot,
          // and rare (one per queue change).
          setServerQueue(
            (msg.items ?? []).map((m: {id: number; text: string}) => ({id: String(m.id), text: m.text})),
          );
          return;
        }
        pending.current.push(msg);
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
  // socket while it still reports OPEN — sends into it vanish and nothing new
  // arrives (history "not loading until restart"). A fresh socket on every
  // resume avoids trusting it; its onopen resyncs.
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

  // Hands the message to the server's queue, which sends it straight away,
  // even while a reply is running. While the socket is down it's held locally (shown as
  // queued) and handed over on reconnect.
  const send = useCallback((text: string) => {
    setState(prev => (prev.error ? {...prev, error: null} : prev));
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({type: 'prompt', text}));
      return;
    }
    const id = `local-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    setUnsentBoth([...unsentRef.current, {id, text, local: true}]);
  }, []);

  // Drops one queued message — for every app on this station, since the
  // queue is the server's. Removed from view right away; the server's next
  // snapshot confirms it (a message that was already sent just stays sent).
  const removeQueued = useCallback((id: string) => {
    if (unsentRef.current.some(m => m.id === id)) {
      setUnsentBoth(unsentRef.current.filter(m => m.id !== id));
      return;
    }
    setServerQueue(q => q.filter(m => m.id !== id));
    wsRef.current?.send(JSON.stringify({type: 'queue_remove', id: Number(id)}));
  }, []);

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
  // there until the next send happens to clear it.
  const dismissError = useCallback(() => {
    setState(prev => (prev.error ? {...prev, error: null} : prev));
  }, []);

  // After a force stop (kill): clear busy without waiting on opencode's
  // turn-finished event, which may never come if this client never saw the
  // run start. The server already emptied the queue as part of the abort;
  // clearing it here too just saves waiting for its snapshot.
  const forceIdle = useCallback(() => {
    setUnsentBoth([]);
    setServerQueue([]);
    setState(prev => ({...prev, busy: false}));
  }, []);

  const reset = useCallback(() => {
    pending.current = [];
    historyCursor.current = '';
    setHasMoreHistory(false);
    setUnsentBoth([]);
    setServerQueue([]);
    setState(emptyChatState);
  }, []);

  // After a Station reset (new opencode session), the open socket's
  // subscription is still keyed to the old session ID — a fresh socket
  // re-opens against the station's *current* session (the server resolves
  // that fresh on every connect).
  const reconnect = useCallback(() => {
    reconnectNowRef.current();
  }, []);

  const queue = [...serverQueue, ...unsent];

  return {
    state,
    status,
    queue,
    send,
    removeQueued,
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
