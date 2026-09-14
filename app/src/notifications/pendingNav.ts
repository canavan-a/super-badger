// Bridges a notification tap into App.tsx's navigation when the app was
// alive-but-backgrounded (not killed) at tap time — notifee delivers that
// case via index.js's onBackgroundEvent, which has no React tree to call
// navigate() on directly. Same tiny get/set/subscribe shape as settingsStore
// (app/src/settings.ts), in-memory only (nothing here needs to survive a
// process restart — the killed-app case is handled separately via
// notifee.getInitialNotification() in App.tsx).
type Listener = (stationId: number) => void;

let pending: number | null = null;
const listeners = new Set<Listener>();

export const pendingNav = {
  get: (): number | null => pending,
  set(stationId: number): void {
    pending = stationId;
    listeners.forEach(l => l(stationId));
  },
  clear(): void {
    pending = null;
  },
  subscribe(listener: Listener): () => void {
    listeners.add(listener);
    return () => listeners.delete(listener);
  },
};
