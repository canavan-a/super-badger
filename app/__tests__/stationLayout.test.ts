import {moveItem, splitLayout} from '../src/components/StationLayoutSettings';

const st = (id: number) => ({kind: 'station', station: {id}} as any);
const divider = {kind: 'divider'} as const;

test('dragging a station below the divider hides it, above it reorders active', () => {
  const items = [st(1), st(2), st(3), divider, st(4)];

  // Station 2 dropped just below the divider → hidden, first in that list.
  expect(splitLayout(moveItem(items, 1, 3))).toEqual({active: [1, 3], hidden: [2, 4]});

  // Hidden station 4 dragged to the top → active, first in rotation.
  expect(splitLayout(moveItem(items, 4, 0))).toEqual({active: [4, 1, 2, 3], hidden: []});

  // Plain reorder within active.
  expect(splitLayout(moveItem(items, 2, 0))).toEqual({active: [3, 1, 2], hidden: [4]});
});

jest.mock('../src/api', () => ({
  listStations: () =>
    Promise.resolve([
      {id: 1, name: 'Alpha', model_id: 'm1', color: '#f00', hidden: false, position: 1},
      {id: 2, name: 'Bravo', model_id: 'm2', color: '#0f0', hidden: true, position: 2},
    ]),
  updateStationLayout: jest.fn(() => Promise.resolve()),
}));

test('renders active stations above the Hidden divider and hidden ones below', async () => {
  const React = require('react');
  const renderer = require('react-test-renderer');
  const {StationLayoutSettings} = require('../src/components/StationLayoutSettings');
  let tree: any;
  await renderer.act(async () => {
    tree = renderer.create(React.createElement(StationLayoutSettings));
  });
  const texts: string[] = tree.root
    .findAll((n: any) => typeof n.type === 'string' && n.type === 'Text')
    .map((n: any) => [].concat(n.props.children).join(''));
  const order = texts.filter(t => ['Active', 'Alpha', 'Hidden', 'Bravo'].includes(t));
  expect(order).toEqual(['Active', 'Alpha', 'Hidden', 'Bravo']);
  await renderer.act(async () => tree.unmount());
});
