// Hand-rolled routing: react-navigation drags in native-only deps
// (react-native-screens/gesture-handler/reanimated) that complicate the
// react-native-web/Vite target for a handful of screens, so App.tsx just
// switches on this union instead.
export type Route =
  | {name: 'stations'}
  | {name: 'stationDetail'; id: number}
  | {name: 'stationSettings'; id: number}
  | {name: 'addStation'}
  | {name: 'settings'};
