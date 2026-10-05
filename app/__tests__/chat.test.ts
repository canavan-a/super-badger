import {applyEvent, emptyChatState, getTurn, prependHistory, seedFromHistory} from '../src/chat';

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
