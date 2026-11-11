// Chat transcript state, built from super-badger's own event schema (see
// server/opencode/translate.go's Event). The server translates opencode's
// live events and its stored messages into the same flat events, so live
// and restored transcripts are built by this one reducer. This mirrors what
// the opencode TUI itself shows: not just the final reply, but every part of
// the harness's work — reasoning, tool calls (with live status), step
// boundaries, compaction markers — rather than a single text bubble.
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
// is paused entirely.
export interface PendingPermission {
  id: string;
  permission: string;
  patterns: string[];
}

export interface QuestionOption {
  label: string;
  description: string;
  value?: string;
}

// One question of a question request.
export interface QuestionItem {
  key: string;
  question: string;
  header: string;
  options: QuestionOption[];
  multiple: boolean;
  // Whether opencode will accept a free-text answer not among `options` —
  // surfaced as an "Other…" field in the UI.
  custom: boolean;
}

// A pending question request (opencode's form) — structurally similar to a
// permission request (opencode pauses the agent loop until answered) but a
// different mechanism. One request can ask several questions; they're shown
// one at a time. The flat QuestionItem fields are the question showing
// (number `index` of `questions.length`), `answers` those answered so far.
// All answers go back together once the last is answered, as the chosen
// option labels (or free text) — see answerQuestion and useStationChat's
// replyQuestion.
export interface PendingQuestion extends QuestionItem {
  id: string;
  index: number;
  questions: QuestionItem[];
  answers: string[][];
}

function showQuestion(id: string, questions: QuestionItem[], answers: string[][]): PendingQuestion {
  return {...questions[answers.length], id, index: answers.length, questions, answers};
}

// Records the answer to the question showing. Returns the next question to
// show, or — once every question is answered — null and all the answers,
// one array per question, in order.
export function answerQuestion(
  q: PendingQuestion,
  labels: string[],
): {next: PendingQuestion | null; all: string[][] | null} {
  const answers = [...q.answers, labels];
  if (answers.length >= q.questions.length) return {next: null, all: answers};
  return {next: showQuestion(q.id, q.questions, answers), all: null};
}

// One step's token usage (see translate.go's Tokens). ctx is what the
// context window holds after the step — the figure the gauge shows.
export interface StepTokens {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
  ctx: number;
}

// The live context gauge: sent as a `usage` event after every step (and on
// connect), and returned by GET /stations/:id/usage in the same shape.
// tokens is absent before the first step and right after a compaction
// (until the next step reports the compacted size).
export interface UsageInfo {
  tokens?: StepTokens;
  cost?: number;
  contextLimit?: number;
}

export interface ChatState {
  turns: Turn[];
  turnIndex: Record<string, number>;
  busy: boolean;
  error: string | null;
  notice: string | null;
  pendingPermission: PendingPermission | null;
  pendingQuestion: PendingQuestion | null;
  usage: UsageInfo | null;
  // Every turn held arrived live since the transcript was last emptied (on
  // open, or a session reset) — none is left over from an older load, so a
  // history snapshot that doesn't have them yet must keep them (see
  // rebuildFromHistory).
  fresh: boolean;
}

export const emptyChatState: ChatState = {
  turns: [],
  turnIndex: {},
  busy: false,
  error: null,
  notice: null,
  pendingPermission: null,
  pendingQuestion: null,
  usage: null,
  fresh: true,
};

// One flat event from the station WebSocket or a history page — see
// translate.go's Event for the authoritative field list. The server's own
// messages (notice, error, queue, session_reset, heartbeat) share the shape.
export type EventMsg = {
  type: string;
  sessionID?: string;
  messageID?: string;
  partID?: string;
  role?: 'user' | 'assistant';
  done?: boolean;
  finish?: string;
  time?: number;
  kind?: string;
  text?: string;
  delta?: string;
  tool?: string;
  status?: string;
  input?: unknown;
  output?: string;
  error?: string;
  phase?: string;
  reason?: string;
  attempt?: number;
  at?: number;
  tokens?: StepTokens;
  cost?: number;
  contextLimit?: number;
  id?: string;
  permission?: string;
  patterns?: string[];
  metadata?: unknown;
  questions?: {
    key?: string;
    question: string;
    header: string;
    options?: QuestionOption[];
    multiple?: boolean;
    custom?: boolean;
  }[];
  clear_error?: boolean;
};

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
  const prev = state.turns[idx];
  const next = fn(prev);
  if (next === prev) return state;
  const turns = state.turns.slice();
  turns[idx] = next;
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

function compactionText(status: string | undefined, error: string | undefined): string {
  switch (status) {
    case 'running':
      return 'Compacting…';
    case 'failed':
      return error ? `Compaction failed: ${error}` : 'Compaction failed';
    default:
      return 'Conversation compacted';
  }
}

// Maps a non-text `part` event to the Part the UI renders. text/reasoning
// are handled separately (they stream). null: nothing to show.
function partFromEvent(evt: EventMsg, id: string): Part | null {
  switch (evt.kind) {
    case 'tool': {
      const status = evt.status as ToolStatus | undefined;
      return {
        kind: 'tool',
        id,
        tool: evt.tool || 'tool',
        status: status ?? 'pending',
        input: evt.input,
        output: evt.output,
        error: evt.error,
      };
    }
    case 'step':
      // ctx (input + output + cache) is the closest match to opencode 1.x's
      // tokens.total that this Part used to carry.
      return {kind: 'step-finish', id, tokens: evt.tokens?.ctx};
    case 'compaction':
      return {kind: 'notice', id, text: compactionText(evt.status, evt.error)};
    case 'notice':
      return {kind: 'notice', id, text: evt.text ?? ''};
    default:
      return null;
  }
}

// Whether `next` would only move a text part backward to a prefix of what's
// already shown — see applyPart.
function isStalePrefix(existing: string, next: string): boolean {
  return existing.length > next.length && existing.startsWith(next);
}

const RETRY_PREFIX = 'Retrying (attempt';
const COMPACTING_NOTICE = 'Compacting…';

// Notices that track run state (set/cleared by status and compaction
// events) rather than a one-off message — content arriving doesn't clear
// these the way it does a server notice.
function isProgressNotice(notice: string): boolean {
  return notice === COMPACTING_NOTICE || notice.startsWith(RETRY_PREFIX);
}

function applyPart(state: ChatState, evt: EventMsg): ChatState {
  const {messageID, partID} = evt;
  if (!messageID || !partID) return state;
  // A part can arrive before its turn's msg (a delta for a turn this client
  // joined mid-reply) — assume assistant; a later msg corrects the role.
  let next = ensureTurn(state, messageID, 'assistant');
  const existing = next.turns[next.turnIndex[messageID]].parts[partID];

  if (evt.kind === 'text' || evt.kind === 'reasoning') {
    const kind = evt.kind;
    const prevText = existing && 'text' in existing ? existing.text : undefined;
    if (evt.delta !== undefined && evt.text === undefined) {
      // A delta for a part this client never saw start creates it.
      const text = (prevText ?? '') + evt.delta;
      return updateTurn(next, messageID, t => upsertPart(t, {kind, id: partID, text}));
    }
    if (evt.text === undefined) {
      // "started" (or an "ended" carrying no text): make sure the part
      // exists, but never blank out what has already streamed into it.
      if (existing) return next;
      return updateTurn(next, messageID, t => upsertPart(t, {kind, id: partID, text: ''}));
    }
    // Full text. seedFromHistory replays history events through this same
    // reducer, and can resolve after live deltas have already grown this
    // part past what the history snapshot had at fetch time (the snapshot is
    // always some prefix of the eventually-live text). Applying that stale
    // prefix would visibly snap the text backward and then forward again as
    // later deltas continue, plus yank the scroll position. Checking for an
    // actual prefix (not just "shorter") keeps this from also swallowing a
    // genuine rewrite that happens to be shorter than what's there.
    if (prevText !== undefined && isStalePrefix(prevText, evt.text)) {
      return next;
    }
    const text = evt.text;
    next = updateTurn(next, messageID, t => upsertPart(t, {kind, id: partID, text}));
    return next.notice && !isProgressNotice(next.notice) ? {...next, notice: null} : next;
  }

  const part = partFromEvent(evt, partID);
  if (!part) return next;
  return updateTurn(next, messageID, t => upsertPart(t, part));
}

export function applyEvent(state: ChatState, evt: EventMsg): ChatState {
  switch (evt.type) {
    case 'msg': {
      const {messageID} = evt;
      if (!messageID) return state;
      const role = evt.role === 'user' ? 'user' : 'assistant';
      const next = ensureTurn(state, messageID, role);
      return updateTurn(next, messageID, t => {
        let turn = t;
        if (turn.role !== role) turn = {...turn, role};
        // done only ever goes false -> true: history and live can deliver a
        // turn's start after its end.
        if (evt.done && !turn.done) turn = {...turn, done: true};
        // A failed step carries its error here — the only trace of it in
        // history, so it's kept on the turn rather than just the banner.
        if (evt.error && evt.finish !== 'aborted') {
          turn = upsertPart(turn, {kind: 'notice', id: `${messageID}/error`, text: `Error: ${evt.error}`});
        }
        return turn;
      });
    }

    case 'part':
      return applyPart(state, evt);

    // The one source of truth for busy: sent live as runs start and end, by
    // the server on every socket connect (see server/api/ws.go), and
    // re-fetched on resync — so a reply whose end this client missed can't
    // leave the send button spinning.
    case 'status': {
      const busy = evt.status === 'busy' || evt.status === 'retry';
      let next = state.busy === busy ? state : {...state, busy};
      if (evt.status === 'retry') {
        const n = evt.attempt ? ` ${evt.attempt}` : '';
        next = {...next, notice: `${RETRY_PREFIX}${n})${evt.error ? `: ${evt.error}` : ''}`};
      } else if (next.notice?.startsWith(RETRY_PREFIX)) {
        next = {...next, notice: null};
      }
      return next;
    }

    // Live compaction progress. The transcript marker itself is a
    // `compaction` part (history); this drives the banner meanwhile.
    case 'compaction':
      switch (evt.phase) {
        case 'started':
          return {...state, notice: COMPACTING_NOTICE};
        case 'ended':
          // The old context figure overstates the compacted one; the next
          // step's usage reports the real size.
          return {
            ...state,
            notice: 'Conversation compacted',
            usage: state.usage ? {...state.usage, tokens: undefined} : state.usage,
          };
        default:
          // failed: the server sends its own notice/error for this.
          return state.notice === COMPACTING_NOTICE ? {...state, notice: null} : state;
      }

    case 'usage':
      return {
        ...state,
        usage: {
          tokens: evt.tokens,
          cost: evt.cost ?? state.usage?.cost,
          contextLimit: evt.contextLimit || state.usage?.contextLimit,
        },
      };

    case 'permission.asked': {
      const {id, permission, patterns} = evt;
      if (!id) return state;
      return {...state, pendingPermission: {id, permission: permission ?? '', patterns: patterns ?? []}};
    }

    // Only the request it's for: parallel tool calls can ask more than one,
    // and the newer replaces the older here, so a reply to the older must
    // not clear it. (No id: an older server; clear.)
    case 'permission.replied':
      if (!state.pendingPermission || (evt.id && evt.id !== state.pendingPermission.id)) return state;
      return {...state, pendingPermission: null};

    case 'question.asked': {
      const {id, questions} = evt;
      if (!id || !questions?.length) return state;
      // The same request again (re-pushed on reconnect): keep the answers
      // already given.
      if (state.pendingQuestion?.id === id) return state;
      const items: QuestionItem[] = questions.map(q => ({
        key: q.key ?? '',
        question: q.question,
        header: q.header,
        options: q.options ?? [],
        multiple: !!q.multiple,
        custom: !!q.custom,
      }));
      return {...state, pendingQuestion: showQuestion(id, items, [])};
    }

    case 'question.replied':
    case 'question.rejected':
      if (!state.pendingQuestion || (evt.id && evt.id !== state.pendingQuestion.id)) return state;
      return {...state, pendingQuestion: null};

    // Both opencode's errors ({error, kind}) and the server's own (a prompt
    // failing outright). Only the banner: busy follows `status`.
    case 'error':
      console.log('[chat] error', JSON.stringify(evt));
      return {...state, error: evt.error ?? 'unknown error'};

    // Server-synthesized: e.g. "Session expired — starting a new one…"
    // while promptWithRecovery transparently resets and retries.
    // clear_error: the notice is the server handling that error itself (auto
    // mode compacting after a context overflow), so its banner goes.
    case 'notice':
      return {...state, notice: evt.text ?? null, ...(evt.clear_error ? {error: null} : {})};

    // Server-synthesized: sent right after the station moved to a new
    // session (rollover, or an expired one replaced). The old session's
    // turns are discarded rather than left mixed in with the new session's —
    // matches what a manual reset already does client-side. The model (so
    // its context limit) is unchanged. useStationChat then reloads the new
    // session's history, which has what was sent before this arrived (the
    // carried-over context).
    case 'session_reset':
      return {
        ...emptyChatState,
        notice: state.notice,
        usage: state.usage?.contextLimit ? {contextLimit: state.usage.contextLimit} : null,
      };

    // title: no place to show it. resync: handled by useStationChat (it
    // needs fetches, not a state change).
    default:
      return state;
  }
}

export function getTurn(state: ChatState, messageID: string): Turn | undefined {
  const idx = state.turnIndex[messageID];
  return idx === undefined ? undefined : state.turns[idx];
}

// Replays cached history events (see api.getStationHistory) on top of
// whatever's already in state — used once, on mount, before live WS events
// start arriving. Order-independent by construction (every event is an
// idempotent upsert keyed by ID), so it's safe to call whether history
// resolves before or after the first live events do.
export function seedFromHistory(state: ChatState, events: EventMsg[]): ChatState {
  return events.reduce(applyEvent, state);
}

// Puts an older page of history (see api.getStationHistory's `before`)
// *above* the turns already shown. seedFromHistory can't do this: applyEvent
// appends any turn it hasn't seen, which would land older messages below
// newer ones. So the page is built on its own and its turns spliced in
// front; turns already present are skipped (pages are disjoint by message,
// so that's only ever a defensive no-op).
export function prependHistory(state: ChatState, events: EventMsg[]): ChatState {
  const older = seedFromHistory(emptyChatState, events).turns.filter(
    t => state.turnIndex[t.messageID] === undefined,
  );
  if (older.length === 0) return state;
  const turns = [...older, ...state.turns];
  const turnIndex: Record<string, number> = {};
  turns.forEach((t, i) => {
    turnIndex[t.messageID] = i;
  });
  return {...state, turns, turnIndex};
}

// All turns from restored history, in original order, both roles — history
// has no optimistic local echo to duplicate against.
export function allTurns(state: ChatState): Turn[] {
  return state.turns;
}

// The plain text of a turn's text parts (no reasoning/tool output) — what the
// long-press "select text" view shows.
export function turnText(turn: Turn): string {
  return turn.partOrder
    .map(id => turn.parts[id])
    .filter((p): p is Extract<Part, {kind: 'text'}> => p?.kind === 'text')
    .map(p => p.text)
    .join('\n\n');
}

// Throws away whatever's held locally and rebuilds from a fresh newest page
// of history — used whenever the socket (re)connects, the app comes back to
// the foreground, or the server reports its upstream stream reconnected
// (`resync`). Anything that happened meanwhile never reached this client
// live, and patching it in on top of stale local state is what left chats
// half-loaded, out of order or flashing until the app was restarted; a
// clean rebuild is the same state a fresh open would have. Older pages are
// dropped too and come back through loadOlder on scroll.
//
// What survives from local state is what arrived live after the history
// snapshot was taken:
// - turns the snapshot doesn't have yet: those after the newest turn both
//   share, or all of them while the state is `fresh` (nothing in it is left
//   over from an older load, so a turn missing from the snapshot is newer);
// - in a turn both have: live text that's grown past the snapshot's prefix
//   of the same part (see applyPart's stale-prefix comment), parts that
//   started after the snapshot, and a done the snapshot hadn't seen.
export function rebuildFromHistory(prev: ChatState, events: EventMsg[]): ChatState {
  const base = seedFromHistory(emptyChatState, events);
  const turns = base.turns.map(t => {
    const old = getTurn(prev, t.messageID);
    if (!old) return t;
    let parts = t.parts;
    let partOrder = t.partOrder;
    for (const id of old.partOrder) {
      const snap = t.parts[id];
      const live = old.parts[id];
      if (!snap) {
        parts = {...parts, [id]: live};
        partOrder = [...partOrder, id];
      } else if ('text' in live && 'text' in snap && isStalePrefix(live.text, snap.text)) {
        parts = {...parts, [id]: live};
      }
    }
    const done = t.done || old.done;
    return parts === t.parts && done === t.done ? t : {...t, parts, partOrder, done};
  });

  let lastShared = -1;
  prev.turns.forEach((t, i) => {
    if (base.turnIndex[t.messageID] !== undefined) lastShared = i;
  });
  // Nothing shared and not fresh: all of prev is from before the snapshot.
  const live = prev.fresh ? prev.turns : lastShared >= 0 ? prev.turns.slice(lastShared + 1) : [];
  for (const t of live) {
    if (base.turnIndex[t.messageID] === undefined) turns.push(t);
  }
  const turnIndex: Record<string, number> = {};
  turns.forEach((t, i) => {
    turnIndex[t.messageID] = i;
  });

  // The "Compacting…" banner is cleared by the compaction's end event; if
  // that was missed, history says whether it's still running.
  const notice = prev.notice === COMPACTING_NOTICE && !compactionRunning(turns) ? null : prev.notice;

  // busy is deliberately left alone: guessing it from the newest turn here
  // got it wrong (stuck spinner). The `status` the server sends on every
  // connect (or the status fetch on resync) sets it instead.
  return {
    ...prev,
    turns,
    turnIndex,
    notice,
    // Still fresh until a snapshot has something in it to anchor on.
    fresh: prev.fresh && base.turns.length === 0,
    pendingPermission: prev.pendingPermission ?? base.pendingPermission,
    pendingQuestion: prev.pendingQuestion ?? base.pendingQuestion,
  };
}

// Whether the newest compaction marker among turns is still running.
function compactionRunning(turns: Turn[]): boolean {
  for (let i = turns.length - 1; i >= 0; i--) {
    const t = turns[i];
    for (let j = t.partOrder.length - 1; j >= 0; j--) {
      const id = t.partOrder[j];
      if (id.endsWith('/compaction')) {
        const p = t.parts[id];
        return p?.kind === 'notice' && p.text === COMPACTING_NOTICE;
      }
    }
  }
  return false;
}
