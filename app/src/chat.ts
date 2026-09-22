// Chat transcript state, built from opencode's own event stream (forwarded
// verbatim by the server's per-station WebSocket — see
// server/api/ws.go / server/opencode/events.go). This mirrors what the
// opencode TUI itself shows: not just the final reply, but every part of the
// harness's work — reasoning, tool calls (with live status), step
// boundaries, file changes, etc. — rather than a single text bubble.
//
// react-native's global WebSocket works the same on native and web, so this
// one hook drives both — no platform split needed (unlike SSE, which has no
// built-in RN client).

export type ToolStatus = 'pending' | 'running' | 'completed' | 'error';

export type Part =
  | {kind: 'text'; id: string; text: string}
  | {kind: 'reasoning'; id: string; text: string}
  | {
      kind: 'tool';
      id: string;
      tool: string;
      status: ToolStatus;
      title?: string;
      input?: unknown;
      output?: string;
      error?: string;
    }
  | {kind: 'step-finish'; id: string; tokens?: number}
  | {kind: 'file'; id: string; label: string}
  | {kind: 'subtask'; id: string; label: string}
  | {kind: 'notice'; id: string; text: string};

export interface Turn {
  messageID: string;
  role: 'user' | 'assistant';
  partOrder: string[];
  parts: Record<string, Part>;
  done: boolean;
}

// A pending tool-permission request opencode is blocked on — until this is
// answered (see useStationChat's replyPermission), that session's agent loop
// is paused entirely. This is what "stuck on read" was: nothing was ever
// showing this or letting the user respond to it.
export interface PendingPermission {
  id: string;
  permission: string;
  patterns: string[];
}

// A pending question from opencode's separate AskUserQuestion-style tool —
// structurally similar to a permission request (opencode pauses the agent
// loop until answered) but a different mechanism/API entirely
// (question.asked/question.replied, GET /question, POST /question/{id}/reply
// — see server/opencode/client.go). A QuestionRequest can technically carry
// more than one question at once; only the first is surfaced here, which
// covers how this tool is actually used in practice.
export interface PendingQuestion {
  id: string;
  question: string;
  header: string;
  options: {label: string; description: string}[];
  multiple: boolean;
  // Whether opencode will accept a free-text answer not among `options` —
  // surfaced as an "Other…" field in the UI.
  custom: boolean;
}

export interface ChatState {
  turns: Turn[];
  turnIndex: Record<string, number>;
  busy: boolean;
  error: string | null;
  notice: string | null;
  pendingPermission: PendingPermission | null;
  pendingQuestion: PendingQuestion | null;
}

export const emptyChatState: ChatState = {
  turns: [],
  turnIndex: {},
  busy: false,
  error: null,
  notice: null,
  pendingPermission: null,
  pendingQuestion: null,
};

// Most messages on this socket are opencode's own events (nested under
// `properties`), but the server also sends a couple of its own synthetic,
// flat messages: {type:"error", error} when a prompt fails outright, and
// {type:"notice", text} when it's transparently recovering something (e.g.
// re-creating a session opencode dropped) — see server/api/ws.go.
export type EventMsg = {id?: string; type: string; properties?: any; error?: string; text?: string};

function ensureTurn(state: ChatState, messageID: string, role: 'user' | 'assistant'): ChatState {
  if (state.turnIndex[messageID] !== undefined) {
    return state;
  }
  const turn: Turn = {messageID, role, partOrder: [], parts: {}, done: false};
  const turns = [...state.turns, turn];
  return {...state, turns, turnIndex: {...state.turnIndex, [messageID]: turns.length - 1}};
}

function updateTurn(state: ChatState, messageID: string, fn: (t: Turn) => Turn): ChatState {
  const idx = state.turnIndex[messageID];
  if (idx === undefined) {
    return state;
  }
  const turns = state.turns.slice();
  turns[idx] = fn(turns[idx]);
  return {...state, turns};
}

function upsertPart(turn: Turn, part: Part): Turn {
  const isNew = turn.parts[part.id] === undefined;
  return {
    ...turn,
    parts: {...turn.parts, [part.id]: part},
    partOrder: isNew ? [...turn.partOrder, part.id] : turn.partOrder,
  };
}

function partFromRaw(raw: any): Part | null {
  const id: string = raw.id;
  switch (raw.type) {
    case 'text':
      return {kind: 'text', id, text: raw.text ?? ''};
    case 'reasoning':
      return {kind: 'reasoning', id, text: raw.text ?? ''};
    case 'tool': {
      const state = raw.state ?? {};
      return {
        kind: 'tool',
        id,
        tool: raw.tool ?? 'tool',
        status: state.status ?? 'pending',
        title: state.title,
        input: state.input,
        output: state.output,
        error: state.error,
      };
    }
    case 'step-finish':
      return {kind: 'step-finish', id, tokens: raw.tokens?.total};
    case 'step-start':
      return null; // no useful content to show; step-finish carries the summary
    case 'file':
    case 'patch':
    case 'snapshot':
      return {kind: 'file', id, label: raw.filename ?? raw.path ?? raw.type};
    case 'agent':
    case 'subtask':
      return {kind: 'subtask', id, label: raw.description ?? raw.agent ?? 'subtask'};
    case 'retry':
      return {kind: 'notice', id, text: 'Retrying…'};
    case 'compaction':
      return {kind: 'notice', id, text: 'Conversation compacted'};
    default:
      return {kind: 'notice', id, text: `[${raw.type}]`};
  }
}

export function applyEvent(state: ChatState, evt: EventMsg): ChatState {
  const p = evt.properties ?? {};

  switch (evt.type) {
    case 'message.updated': {
      const info = p.info;
      if (!info) return state;
      let next = ensureTurn(state, info.id, info.role);
      if (info.time?.completed) {
        next = updateTurn(next, info.id, t => ({...t, done: true}));
        // session.idle is opencode's dedicated "done" signal, but it isn't
        // reliably the thing that fires here (or reliably survives a
        // reconnect's history replay) — a completed assistant message is
        // just as good a "the reply is finished" signal and is what was
        // actually leaving the send button stuck showing its busy spinner
        // indefinitely when session.idle didn't arrive.
        if (info.role === 'assistant') {
          next = {...next, busy: false};
        }
      }
      return next;
    }

    case 'message.part.updated': {
      const rawPart = p.part;
      if (!rawPart) return state;
      const part = partFromRaw(rawPart);
      if (!part) return state;
      // messageID's role isn't known yet if message.updated hasn't arrived
      // first — assume assistant, corrected (harmlessly, same messageID) if
      // a later message.updated says otherwise.
      let next = ensureTurn(state, rawPart.messageID, 'assistant');
      // seedFromHistory (below) replays cached history events through this
      // same reducer, and can resolve after live message.part.delta events
      // have already grown this part past what the history snapshot had at
      // fetch time (server merges deltas into the part's slot rather than
      // caching them separately, so the snapshot is always some prefix of
      // the eventually-live text). Applying that stale prefix would visibly
      // snap the text backward and then forward again as later deltas
      // continue, plus yank the scroll position via the resulting
      // content-height dip. Checking for an actual prefix (not just
      // "shorter") is what keeps this from also swallowing a genuine
      // rewrite/revert that happens to be shorter than what's there.
      const existingPart = next.turns[next.turnIndex[rawPart.messageID]]?.parts[part.id];
      if (
        existingPart &&
        'text' in existingPart &&
        'text' in part &&
        (existingPart.text ?? '').length > (part.text ?? '').length &&
        (existingPart.text ?? '').startsWith(part.text ?? '')
      ) {
        return next.notice ? {...next, notice: null} : next;
      }
      next = updateTurn(next, rawPart.messageID, t => upsertPart(t, part));
      return next.notice ? {...next, notice: null} : next;
    }

    case 'message.part.delta': {
      const {messageID, partID, field, delta} = p;
      if (!messageID || !partID) return state;
      const idx = state.turnIndex[messageID];
      if (idx === undefined) return state;
      if (field !== 'text') return state; // the only delta field opencode sends today
      return updateTurn(state, messageID, t => {
        const existing = t.parts[partID];
        if (!existing || !('text' in existing)) return t;
        return upsertPart(t, {...existing, text: (existing.text ?? '') + delta} as Part);
      });
    }

    case 'session.idle':
      return {...state, busy: false};

    case 'permission.asked': {
      const {id, permission, patterns} = p;
      if (!id) return state;
      return {...state, pendingPermission: {id, permission, patterns: patterns ?? []}};
    }

    case 'permission.replied':
      // Clears regardless of which request this was for — a station only
      // ever has one session, so only one permission can be pending at once.
      return {...state, pendingPermission: null};

    case 'question.asked': {
      const {id, questions} = p;
      const q = questions?.[0];
      if (!id || !q) return state;
      return {
        ...state,
        pendingQuestion: {
          id,
          question: q.question,
          header: q.header,
          options: q.options ?? [],
          multiple: !!q.multiple,
          custom: !!q.custom,
        },
      };
    }

    case 'question.replied':
    case 'question.rejected':
      return {...state, pendingQuestion: null};

    case 'session.error': {
      const message = p.error?.message ?? p.message ?? 'session error';
      // Logged (not just surfaced in the UI) so the raw event is visible in
      // `adb logcat`/Metro without having to reproduce it against the
      // server directly — this is opencode's own error payload verbatim.
      console.log('[chat] session.error', JSON.stringify(evt));
      return {...state, busy: false, error: message};
    }

    // Server-synthesized (see server/api/ws.go), not an opencode event.
    case 'error':
      console.log('[chat] error', JSON.stringify(evt));
      return {...state, busy: false, error: evt.error ?? 'unknown error'};

    // Server-synthesized: e.g. "Session expired — starting a new one…"
    // while promptWithRecovery transparently resets and retries.
    case 'notice':
      return {...state, notice: evt.text ?? null};

    // Server-synthesized: sent right after an auto-recovery reset. The old
    // session (and everything shown from it) no longer exists, so it's
    // discarded outright rather than left mixed in with the new session's
    // turns — matches what a manual reset already does client-side.
    case 'session_reset':
      return {...emptyChatState, notice: state.notice};

    default:
      return state;
  }
}

export function getTurn(state: ChatState, messageID: string): Turn | undefined {
  const idx = state.turnIndex[messageID];
  return idx === undefined ? undefined : state.turns[idx];
}

// Only assistant turns are rendered from the *live* event stream — the
// user's own message is shown immediately, optimistically, by the screen
// itself the moment they hit send (see StationDetailScreen), so echoing the
// WS-reported user turn back would duplicate it. Restored history (below)
// has no such optimistic echo to duplicate, so it includes both roles.
export function assistantTurns(state: ChatState): Turn[] {
  return state.turns.filter(t => t.role === 'assistant');
}

// Replays cached history events (see api.getStationHistory) on top of
// whatever's already in state — used once, on mount, before live WS events
// start arriving. Order-independent by construction (every event is an
// idempotent upsert keyed by ID), so it's safe to call whether history
// resolves before or after the first live events do.
export function seedFromHistory(state: ChatState, events: EventMsg[]): ChatState {
  return events.reduce(applyEvent, state);
}

// All turns from restored history, in original order, both roles — history
// has no optimistic local echo to duplicate against.
export function allTurns(state: ChatState): Turn[] {
  return state.turns;
}
