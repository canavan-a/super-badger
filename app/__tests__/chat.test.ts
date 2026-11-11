import {
  answerQuestion,
  applyEvent,
  ChatState,
  emptyChatState,
  EventMsg,
  getTurn,
  prependHistory,
  rebuildFromHistory,
  seedFromHistory,
} from '../src/chat';

const msg = (messageID: string, role: 'user' | 'assistant', extra: Partial<EventMsg> = {}): EventMsg => ({
  type: 'msg',
  messageID,
  role,
  ...extra,
});
const textPart = (messageID: string, partID: string, text: string): EventMsg => ({
  type: 'part',
  messageID,
  partID,
  kind: 'text',
  text,
});
const delta = (messageID: string, partID: string, d: string, kind = 'text'): EventMsg => ({
  type: 'part',
  messageID,
  partID,
  kind,
  delta: d,
});
const done: Partial<EventMsg> = {done: true, finish: 'stop'};
const apply = (state: ChatState, ...events: EventMsg[]) => events.reduce(applyEvent, state);

test('a stale (shorter) history replay does not clobber already-streamed text', () => {
  let state = apply(emptyChatState, msg('m1', 'assistant'), textPart('m1', 'p1', 'Hello wor'), delta('m1', 'p1', 'ld'));
  // A late-arriving history snapshot for the same part, taken before the
  // delta above was sent.
  state = applyEvent(state, textPart('m1', 'p1', 'Hello wor'));
  expect(getTurn(state, 'm1')?.parts.p1).toMatchObject({text: 'Hello world'});
});

test('a genuine shorter rewrite still replaces the text', () => {
  const state = apply(emptyChatState, textPart('m1', 'p1', 'Hello world'), textPart('m1', 'p1', 'Bye'));
  expect(getTurn(state, 'm1')?.parts.p1).toMatchObject({text: 'Bye'});
});

test('delta streaming: started, deltas, ended', () => {
  let state = apply(
    emptyChatState,
    msg('m1', 'assistant'),
    {type: 'part', messageID: 'm1', partID: 'm1/reasoning/0', kind: 'reasoning'},
    delta('m1', 'm1/reasoning/0', 'Think', 'reasoning'),
    delta('m1', 'm1/reasoning/0', 'ing', 'reasoning'),
    {type: 'part', messageID: 'm1', partID: 'm1/text/0', kind: 'text'},
    delta('m1', 'm1/text/0', 'Hel'),
    delta('m1', 'm1/text/0', 'lo'),
  );
  let turn = getTurn(state, 'm1')!;
  expect(turn.partOrder).toEqual(['m1/reasoning/0', 'm1/text/0']);
  expect(turn.parts['m1/reasoning/0']).toEqual({kind: 'reasoning', id: 'm1/reasoning/0', text: 'Thinking'});
  expect(turn.parts['m1/text/0']).toEqual({kind: 'text', id: 'm1/text/0', text: 'Hello'});

  // A second "started" for a part that has streamed doesn't blank it.
  state = applyEvent(state, {type: 'part', messageID: 'm1', partID: 'm1/text/0', kind: 'text'});
  expect(getTurn(state, 'm1')?.parts['m1/text/0']).toMatchObject({text: 'Hello'});

  // ended carries the full text, replacing.
  state = applyEvent(state, textPart('m1', 'm1/text/0', 'Hello!'));
  turn = getTurn(state, 'm1')!;
  expect(turn.parts['m1/text/0']).toMatchObject({text: 'Hello!'});
});

test('a delta for an unseen turn and part creates both (assistant)', () => {
  const state = applyEvent(emptyChatState, delta('m9', 'm9/text/0', 'mid-reply'));
  const turn = getTurn(state, 'm9');
  expect(turn?.role).toBe('assistant');
  expect(turn?.parts['m9/text/0']).toEqual({kind: 'text', id: 'm9/text/0', text: 'mid-reply'});
});

test('a later msg corrects the role of a turn created by a part', () => {
  const state = apply(emptyChatState, textPart('u1', 'u1/text/0', 'hi'), msg('u1', 'user', {done: true}));
  expect(getTurn(state, 'u1')?.role).toBe('user');
  expect(getTurn(state, 'u1')?.done).toBe(true);
});

test('tool lifecycle: pending, running, completed are upserts of one part', () => {
  const tool = (extra: Partial<EventMsg>): EventMsg => ({
    type: 'part',
    messageID: 'm1',
    partID: 'call1',
    kind: 'tool',
    tool: 'bash',
    ...extra,
  });
  let state = apply(emptyChatState, msg('m1', 'assistant'), tool({status: 'pending'}));
  expect(getTurn(state, 'm1')?.parts.call1).toMatchObject({kind: 'tool', tool: 'bash', status: 'pending'});

  state = applyEvent(state, tool({status: 'running', input: {command: 'ls'}}));
  expect(getTurn(state, 'm1')?.parts.call1).toMatchObject({status: 'running', input: {command: 'ls'}});

  state = applyEvent(state, tool({status: 'completed', input: {command: 'ls'}, output: 'a\nb'}));
  const turn = getTurn(state, 'm1')!;
  expect(turn.partOrder).toEqual(['call1']);
  expect(turn.parts.call1).toMatchObject({status: 'completed', output: 'a\nb'});

  state = applyEvent(state, {...tool({status: 'error', error: 'boom'}), partID: 'call2', tool: ''});
  expect(getTurn(state, 'm1')?.parts.call2).toMatchObject({tool: 'tool', status: 'error', error: 'boom'});
});

test('step parts map to step-finish with the ctx token figure', () => {
  const state = applyEvent(emptyChatState, {
    type: 'part',
    messageID: 'm1',
    partID: 'm1/step',
    kind: 'step',
    tokens: {input: 10, output: 5, reasoning: 2, cacheRead: 100, cacheWrite: 0, ctx: 115},
  });
  expect(getTurn(state, 'm1')?.parts['m1/step']).toEqual({kind: 'step-finish', id: 'm1/step', tokens: 115});
});

test('a failed step keeps its error on the turn; an aborted one does not', () => {
  let state = applyEvent(emptyChatState, msg('m1', 'assistant', {done: true, finish: 'error', error: 'model exploded'}));
  expect(getTurn(state, 'm1')?.parts['m1/error']).toMatchObject({kind: 'notice', text: 'Error: model exploded'});
  state = applyEvent(state, msg('m2', 'assistant', {done: true, finish: 'aborted', error: 'aborted'}));
  expect(getTurn(state, 'm2')?.partOrder).toEqual([]);
});

test('busy follows status only, not message completion', () => {
  let state = applyEvent(emptyChatState, {type: 'status', status: 'busy'});
  expect(state.busy).toBe(true);
  // A step ending (even a final one) doesn't clear busy.
  state = apply(state, msg('a1', 'assistant'), msg('a1', 'assistant', done));
  expect(state.busy).toBe(true);
  state = applyEvent(state, {type: 'status', status: 'idle'});
  expect(state.busy).toBe(false);
  // Nor does a reply starting set it.
  state = applyEvent(state, msg('a2', 'assistant'));
  expect(state.busy).toBe(false);
  // An error sets the banner but leaves busy alone.
  state = apply(state, {type: 'status', status: 'busy'}, {type: 'error', error: 'overflow', kind: 'overflow'} as EventMsg);
  expect(state.busy).toBe(true);
  expect(state.error).toBe('overflow');
});

test('retry status is busy and shows a notice, cleared when the run resumes', () => {
  let state = applyEvent(emptyChatState, {type: 'status', status: 'retry', attempt: 2, error: 'rate limited'});
  expect(state.busy).toBe(true);
  expect(state.notice).toBe('Retrying (attempt 2): rate limited');
  state = applyEvent(state, {type: 'status', status: 'busy'});
  expect(state.busy).toBe(true);
  expect(state.notice).toBeNull();
});

test('compaction parts are transcript markers', () => {
  const part = (status: string, error?: string): EventMsg => ({
    type: 'part',
    messageID: 'c1',
    partID: 'c1/compaction',
    kind: 'compaction',
    status,
    reason: 'auto',
    error,
  });
  let state = apply(emptyChatState, msg('c1', 'assistant'), part('running'));
  expect(getTurn(state, 'c1')?.parts['c1/compaction']).toMatchObject({kind: 'notice', text: 'Compacting…'});
  state = apply(state, part('completed'), msg('c1', 'assistant', {done: true}));
  expect(getTurn(state, 'c1')?.parts['c1/compaction']).toMatchObject({text: 'Conversation compacted'});
  expect(getTurn(state, 'c1')?.done).toBe(true);
  state = applyEvent(state, part('failed', 'no room'));
  expect(getTurn(state, 'c1')?.parts['c1/compaction']).toMatchObject({text: 'Compaction failed: no room'});
});

test('notice parts (synthetic user turn) are kept', () => {
  const state = apply(emptyChatState, msg('s1', 'user', {done: true}), {
    type: 'part',
    messageID: 's1',
    partID: 's1/notice',
    kind: 'notice',
    text: 'Context carried over from the previous session',
  });
  expect(getTurn(state, 's1')?.parts['s1/notice']).toEqual({
    kind: 'notice',
    id: 's1/notice',
    text: 'Context carried over from the previous session',
  });
});

test('compaction events drive the notice and reset the gauge', () => {
  let state = applyEvent(emptyChatState, {
    type: 'usage',
    tokens: {input: 1, output: 1, reasoning: 0, cacheRead: 0, cacheWrite: 0, ctx: 9000},
    cost: 0,
    contextLimit: 12000,
  });
  state = applyEvent(state, {type: 'compaction', phase: 'started', reason: 'auto'});
  expect(state.notice).toBe('Compacting…');
  // Streaming content mid-compaction doesn't clear the progress notice.
  state = applyEvent(state, textPart('m1', 'p1', 'summary'));
  expect(state.notice).toBe('Compacting…');
  state = applyEvent(state, {type: 'compaction', phase: 'ended', reason: 'auto'});
  expect(state.notice).toBe('Conversation compacted');
  expect(state.usage).toEqual({tokens: undefined, cost: 0, contextLimit: 12000});

  state = apply(state, {type: 'compaction', phase: 'started'}, {type: 'compaction', phase: 'failed', error: 'x'});
  expect(state.notice).toBeNull();
});

test('usage events update the gauge, keeping a known context limit', () => {
  const tokens = {input: 10, output: 5, reasoning: 0, cacheRead: 7000, cacheWrite: 85, ctx: 7100};
  let state = applyEvent(emptyChatState, {type: 'usage', contextLimit: 12000});
  state = applyEvent(state, {type: 'usage', tokens, cost: 0.5});
  expect(state.usage).toEqual({tokens, cost: 0.5, contextLimit: 12000});
});

test('question.asked keeps key and option values', () => {
  const state = applyEvent(emptyChatState, {
    type: 'question.asked',
    id: 'q1',
    questions: [
      {
        key: 'color',
        question: 'Pick one',
        header: 'Color',
        options: [{label: 'Red', description: 'warm', value: 'red'}],
        multiple: false,
        custom: true,
      },
    ],
  });
  expect(state.pendingQuestion).toMatchObject({
    id: 'q1',
    index: 0,
    key: 'color',
    question: 'Pick one',
    header: 'Color',
    options: [{label: 'Red', description: 'warm', value: 'red'}],
    multiple: false,
    custom: true,
    answers: [],
  });
  expect(state.pendingQuestion?.questions).toHaveLength(1);
  expect(applyEvent(state, {type: 'question.rejected', id: 'q1'}).pendingQuestion).toBeNull();
  expect(applyEvent(state, {type: 'question.replied', id: 'q1'}).pendingQuestion).toBeNull();
});

test('permission.asked / replied', () => {
  const state = applyEvent(emptyChatState, {
    type: 'permission.asked',
    id: 'p1',
    permission: 'edit',
    patterns: ['src/*'],
    metadata: {},
  });
  expect(state.pendingPermission).toEqual({id: 'p1', permission: 'edit', patterns: ['src/*']});
  expect(applyEvent(state, {type: 'permission.replied', id: 'p1'}).pendingPermission).toBeNull();
});

test('resync and title events leave state unchanged (the hook handles resync)', () => {
  const state = seedFromHistory(emptyChatState, [msg('m1', 'user'), textPart('m1', 'p1', 'hi')]);
  expect(applyEvent(state, {type: 'resync'})).toBe(state);
  expect(applyEvent(state, {type: 'title', text: 'A title'})).toBe(state);
});

test('resync rebuild: fresh history replaces stale state, keeping live growth', () => {
  // Live state that missed events: a2's reply was cut off mid-stream.
  const prev = apply(
    {...emptyChatState, busy: true},
    msg('u1', 'user', {done: true}),
    textPart('u1', 'u1/text/0', 'go'),
    msg('a1', 'assistant'),
    delta('a1', 'a1/text/0', 'Partial'),
  );
  const state = rebuildFromHistory(prev, [
    msg('u1', 'user', {done: true}),
    textPart('u1', 'u1/text/0', 'go'),
    msg('a1', 'assistant'),
    textPart('a1', 'a1/text/0', 'Partial reply, finished'),
    msg('a1', 'assistant', done),
  ]);
  expect(getTurn(state, 'a1')?.parts['a1/text/0']).toMatchObject({text: 'Partial reply, finished'});
  expect(getTurn(state, 'a1')?.done).toBe(true);
  // busy is left to the status fetch that accompanies a resync.
  expect(state.busy).toBe(true);
  expect(applyEvent(state, {type: 'status', status: 'idle'}).busy).toBe(false);
});

test('prependHistory puts an older page above the turns already shown', () => {
  let state = seedFromHistory(emptyChatState, [msg('m3', 'user'), textPart('m3', 'p3', 'newest')]);
  state = prependHistory(state, [msg('m1', 'user'), textPart('m1', 'p1', 'old'), msg('m2', 'assistant')]);

  expect(state.turns.map(t => t.messageID)).toEqual(['m1', 'm2', 'm3']);
  expect(state.turnIndex).toEqual({m1: 0, m2: 1, m3: 2});

  // Live events after a prepend still land on the right (shifted) turn.
  state = applyEvent(state, textPart('m3', 'p3b', 'more'));
  expect(state.turns[2].partOrder).toEqual(['p3', 'p3b']);
});

test('rebuildFromHistory drops older pages but keeps live turns newer than the snapshot', () => {
  // Not fresh: these came from an earlier load.
  let prev = seedFromHistory({...emptyChatState, fresh: false}, [msg('old', 'user'), textPart('old', 'po', 'old page')]);
  prev = seedFromHistory(prev, [msg('m1', 'user'), msg('m2', 'assistant', done), msg('m3', 'user')]);

  const state = rebuildFromHistory(prev, [msg('m1', 'user'), msg('m2', 'assistant', done)]);

  expect(state.turns.map(t => t.messageID)).toEqual(['m1', 'm2', 'm3']);
  expect(state.turnIndex).toEqual({m1: 0, m2: 1, m3: 2});
});

test('rebuildFromHistory keeps live text that has grown past the snapshot', () => {
  const prev = seedFromHistory(emptyChatState, [msg('m1', 'assistant'), textPart('m1', 'p1', 'Hello world')]);
  const state = rebuildFromHistory(prev, [msg('m1', 'assistant'), textPart('m1', 'p1', 'Hello')]);
  expect(getTurn(state, 'm1')?.parts.p1).toMatchObject({text: 'Hello world'});
});

test('rebuildFromHistory leaves busy to status', () => {
  const state = rebuildFromHistory(emptyChatState, [msg('m1', 'user'), msg('m2', 'assistant')]);
  expect(state.busy).toBe(false);
});

test('session_reset clears the transcript but keeps the context limit', () => {
  let state = apply(
    emptyChatState,
    {type: 'usage', tokens: {input: 1, output: 1, reasoning: 0, cacheRead: 0, cacheWrite: 0, ctx: 2}, contextLimit: 8192},
    msg('m1', 'user'),
  );
  state = applyEvent(state, {type: 'session_reset'});
  expect(state.turns).toEqual([]);
  expect(state.usage).toEqual({contextLimit: 8192});
});

const twoQuestions: EventMsg = {
  type: 'question.asked',
  id: 'q1',
  questions: [
    {key: 'lang', question: 'Language?', header: 'Lang', options: [{label: 'Go', description: ''}]},
    {key: 'why', question: 'Why?', header: 'Why', options: [], custom: true},
  ],
};

test('a multi-question request is answered one question at a time, all sent together', () => {
  const state = applyEvent(emptyChatState, twoQuestions);
  const q = state.pendingQuestion!;
  expect(q).toMatchObject({index: 0, key: 'lang', question: 'Language?'});

  const first = answerQuestion(q, ['Go']);
  expect(first.all).toBeNull();
  expect(first.next).toMatchObject({id: 'q1', index: 1, key: 'why', question: 'Why?', custom: true});

  const second = answerQuestion(first.next!, ['speed']);
  expect(second.next).toBeNull();
  expect(second.all).toEqual([['Go'], ['speed']]);
});

test('a re-pushed question.asked keeps the answers already given', () => {
  let state = applyEvent(emptyChatState, twoQuestions);
  state = {...state, pendingQuestion: answerQuestion(state.pendingQuestion!, ['Go']).next};
  expect(applyEvent(state, twoQuestions).pendingQuestion?.index).toBe(1);
});

test('replies to another request leave the pending one alone', () => {
  let state = applyEvent(emptyChatState, {type: 'permission.asked', id: 'p2', permission: 'bash'});
  state = applyEvent(state, twoQuestions);
  expect(applyEvent(state, {type: 'permission.replied', id: 'p1'}).pendingPermission?.id).toBe('p2');
  expect(applyEvent(state, {type: 'question.replied', id: 'q0'}).pendingQuestion?.id).toBe('q1');
  expect(applyEvent(state, {type: 'permission.replied', id: 'p2'}).pendingPermission).toBeNull();
});

test('rebuildFromHistory keeps live turns of a fresh state the snapshot does not have yet', () => {
  // A new station: the user sends straight away, and the first history page
  // (empty) resolves after their message arrived live.
  let prev = apply(emptyChatState, msg('u1', 'user', {done: true}), textPart('u1', 'u1/text/0', 'hi'));
  let state = rebuildFromHistory(prev, []);
  expect(state.turns.map(t => t.messageID)).toEqual(['u1']);
  expect(state.fresh).toBe(true);

  // Once a snapshot has turns, the state is anchored on it.
  prev = apply(state, msg('a1', 'assistant'));
  state = rebuildFromHistory(prev, [msg('u1', 'user', {done: true}), textPart('u1', 'u1/text/0', 'hi')]);
  expect(state.turns.map(t => t.messageID)).toEqual(['u1', 'a1']);
  expect(state.fresh).toBe(false);
});

test('rebuildFromHistory after session_reset keeps the new session live turns', () => {
  let prev = apply({...emptyChatState, fresh: false}, msg('old', 'user'));
  prev = apply(prev, {type: 'session_reset'}, msg('n1', 'user', {done: true}));
  const state = rebuildFromHistory(prev, [msg('s1', 'user', {done: true})]);
  expect(state.turns.map(t => t.messageID)).toEqual(['s1', 'n1']);
});

test('rebuildFromHistory keeps parts and done that arrived after the snapshot', () => {
  const prev = apply(
    {...emptyChatState, fresh: false},
    msg('a1', 'assistant'),
    textPart('a1', 'a1/text/0', 'Hello'),
    {type: 'part', messageID: 'a1', partID: 'call1', kind: 'tool', tool: 'bash', status: 'running'},
    msg('a1', 'assistant', done),
  );
  const state = rebuildFromHistory(prev, [msg('a1', 'assistant'), textPart('a1', 'a1/text/0', 'Hello')]);
  const t = getTurn(state, 'a1')!;
  expect(t.partOrder).toEqual(['a1/text/0', 'call1']);
  expect(t.done).toBe(true);
});

test('rebuildFromHistory clears a Compacting banner whose end was missed', () => {
  const prev = apply({...emptyChatState, fresh: false}, {type: 'compaction', phase: 'started'});
  expect(prev.notice).toBe('Compacting…');
  const compaction = (status: string): EventMsg[] => [
    msg('c1', 'assistant', {done: status !== 'running'}),
    {type: 'part', messageID: 'c1', partID: 'c1/compaction', kind: 'compaction', status},
  ];
  expect(rebuildFromHistory(prev, compaction('completed')).notice).toBeNull();
  expect(rebuildFromHistory(prev, compaction('running')).notice).toBe('Compacting…');
});
