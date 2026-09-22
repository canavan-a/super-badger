import {applyEvent, emptyChatState, getTurn} from '../src/chat';

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
