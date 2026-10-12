import {
  addLocalUserTurn,
  applyEvent,
  emptyChatState,
  getTurn,
  prependHistory,
  rebuildFromHistory,
  seedFromHistory,
} from '../src/chat';

test('a stale (shorter) history replay does not clobber already-streamed text', () => {
  let state = emptyChatState;
  state = applyEvent(state, {
    type: 'message.updated',
    properties: {info: {id: 'm1', role: 'assistant'}},
  });
  state = applyEvent(state, {
    type: 'message.part.updated',
    properties: {part: {id: 'p1', messageID: 'm1', type: 'text', text: 'Hello wor'}},
  });
  state = applyEvent(state, {
    type: 'message.part.delta',
    properties: {messageID: 'm1', partID: 'p1', field: 'text', delta: 'ld'},
  });

  // A late-arriving history snapshot for the same part, taken before the
  // delta above was sent, replaying its own message.part.updated.
  state = applyEvent(state, {
    type: 'message.part.updated',
    properties: {part: {id: 'p1', messageID: 'm1', type: 'text', text: 'Hello wor'}},
  });

  const turn = getTurn(state, 'm1');
  expect(turn?.parts['p1']).toMatchObject({text: 'Hello world'});
});

test('prependHistory puts an older page above the turns already shown', () => {
  const msg = (id: string, role: string) => ({type: 'message.updated', properties: {info: {id, role}}});
  const part = (messageID: string, id: string, text: string) => ({
    type: 'message.part.updated',
    properties: {part: {id, messageID, type: 'text', text}},
  });

  let state = seedFromHistory(emptyChatState, [msg('m3', 'user'), part('m3', 'p3', 'newest')]);
  state = prependHistory(state, [msg('m1', 'user'), part('m1', 'p1', 'old'), msg('m2', 'assistant')]);

  expect(state.turns.map(t => t.messageID)).toEqual(['m1', 'm2', 'm3']);
  expect(state.turnIndex).toEqual({m1: 0, m2: 1, m3: 2});

  // Live events after a prepend still land on the right (shifted) turn.
  state = applyEvent(state, part('m3', 'p3b', 'more'));
  expect(state.turns[2].partOrder).toEqual(['p3', 'p3b']);
});

test('a step that ends in tool calls keeps the reply busy until the final step', () => {
  let state = {...emptyChatState, busy: true};
  state = applyEvent(state, {
    type: 'message.updated',
    properties: {info: {id: 'm1', role: 'assistant', finish: 'tool-calls', time: {completed: 1}}},
  });
  expect(state.busy).toBe(true);
  expect(getTurn(state, 'm1')?.done).toBe(true);
  state = applyEvent(state, {
    type: 'message.updated',
    properties: {info: {id: 'm2', role: 'assistant', finish: 'stop', time: {completed: 2}}},
  });
  expect(state.busy).toBe(false);
});

const msg = (id: string, role: string, extra: object = {}) => ({
  type: 'message.updated',
  properties: {info: {id, role, ...extra}},
});
const textPart = (messageID: string, id: string, text: string) => ({
  type: 'message.part.updated',
  properties: {part: {id, messageID, type: 'text', text}},
});
const done = {finish: 'stop', time: {completed: 1}};

test('rebuildFromHistory drops older pages but keeps live turns newer than the snapshot', () => {
  let prev = seedFromHistory(emptyChatState, [msg('old', 'user'), textPart('old', 'po', 'old page')]);
  prev = seedFromHistory(prev, [msg('m1', 'user'), msg('m2', 'assistant', done), msg('m3', 'user')]);

  const {state} = rebuildFromHistory(prev, [msg('m1', 'user'), msg('m2', 'assistant', done)], null);

  expect(state.turns.map(t => t.messageID)).toEqual(['m1', 'm2', 'm3']);
  expect(state.turnIndex).toEqual({m1: 0, m2: 1, m3: 2});
});

test('rebuildFromHistory keeps live text that has grown past the snapshot', () => {
  const prev = seedFromHistory(emptyChatState, [msg('m1', 'assistant'), textPart('m1', 'p1', 'Hello world')]);
  const {state} = rebuildFromHistory(prev, [msg('m1', 'assistant'), textPart('m1', 'p1', 'Hello')], null);
  expect(getTurn(state, 'm1')?.parts.p1).toMatchObject({text: 'Hello world'});
});

test('rebuildFromHistory takes busy from the newest assistant turn', () => {
  const stuck = {...emptyChatState, busy: true};
  const finished = rebuildFromHistory(stuck, [msg('m1', 'user'), msg('m2', 'assistant', done)], null);
  expect(finished.state.busy).toBe(false);

  const running = rebuildFromHistory(emptyChatState, [msg('m1', 'user'), msg('m2', 'assistant')], null);
  expect(running.state.busy).toBe(true);

  const midStep = rebuildFromHistory(
    emptyChatState,
    [msg('m2', 'assistant', {finish: 'tool-calls', time: {completed: 1}})],
    null,
  );
  expect(midStep.state.busy).toBe(true);
});

test('rebuildFromHistory drops the optimistic user turn once its echo is in history', () => {
  const prev = {...addLocalUserTurn(emptyChatState, 'local-1', 'hi there'), busy: true};
  const echoed = rebuildFromHistory(prev, [msg('u1', 'user'), textPart('u1', 'pu', 'hi there')], 'local-1');
  expect(echoed.localEchoed).toBe(true);
  expect(echoed.state.turns.map(t => t.messageID)).toEqual(['u1']);

  const notYet = rebuildFromHistory(prev, [], 'local-1');
  expect(notYet.localEchoed).toBe(false);
  expect(notYet.state.turns.map(t => t.messageID)).toEqual(['local-1']);
  expect(notYet.state.busy).toBe(true);
});

test('session.status idle clears busy', () => {
  const state = applyEvent({...emptyChatState, busy: true}, {type: 'session.status', properties: {status: {type: 'idle'}}});
  expect(state.busy).toBe(false);
});
