// Mirrors ../horus-33/mobile/src/service/PresenceService.ts's shape 1:1,
// adapted to super-badger's single aggregate notifications feed (see
// server/api/ws.go's notificationsWS) instead of horus-33's presence socket.
//
// Only ever imported from native-only code paths: app/index.js (top-level,
// registers the foreground-service task), and dynamic import() from
// App.tsx/SettingsScreen.tsx (both shared with the Vite web build, which
// must never statically import notifee or this file).
import notifee, {AndroidForegroundServiceType} from '@notifee/react-native';

import {handleNotificationMsg} from '../notifications/agentEvents';
import {CH_SERVICE, ensureChannels, NID_FGS} from '../notifications/channels';
import {settingsStore} from '../settings';

function notificationsWsURL(): string {
  const {serverUrl, authToken} = settingsStore.get();
  const base = serverUrl.replace(/^http/, 'ws') + '/notifications/ws';
  // See useStationChat's wsURL — a WebSocket handshake can't carry a custom
  // Authorization header, so the token travels as a query param instead once
  // the server has auth enabled (server/api/auth.go's RequireAuth).
  return authToken ? `${base}?token=${encodeURIComponent(authToken)}` : base;
}

let socket: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let backoffMs = 1000;
const MAX_BACKOFF_MS = 120_000;
let stopped = false;

function connect(): void {
  if (stopped) return;
  const ws = new WebSocket(notificationsWsURL());
  socket = ws;

  ws.onopen = () => {
    backoffMs = 1000;
  };
  ws.onmessage = ev => {
    try {
      const msg = JSON.parse(ev.data);
      void handleNotificationMsg(msg);
    } catch {
      // ignore malformed frames
    }
  };
  ws.onclose = () => {
    if (stopped) return;
    reconnectTimer = setTimeout(connect, backoffMs);
    backoffMs = Math.min(backoffMs * 2, MAX_BACKOFF_MS);
  };
  ws.onerror = () => {
    ws.close();
  };
}

// The function Notifee's foreground service task actually runs. Must never
// resolve — Notifee keeps the Android foreground service alive for as long
// as this promise is pending, and tears it down externally (via
// stopForegroundService, see stopMonitoring) rather than by this returning.
export function backgroundRunner(): Promise<void> {
  return new Promise(() => {
    stopped = false;
    void ensureChannels();
    connect();
  });
}

// Must be called from foreground JS (Android 12+ restricts starting a
// foreground service from the background) — displaying a notification with
// asForegroundService:true is what actually promotes the app to a real
// Android foreground service and triggers Notifee to invoke backgroundRunner.
export async function startMonitoring(): Promise<void> {
  await ensureChannels();
  await notifee.displayNotification({
    id: NID_FGS,
    title: 'super-badger',
    body: 'Watching for agent activity…',
    android: {
      channelId: CH_SERVICE,
      asForegroundService: true,
      foregroundServiceTypes: [AndroidForegroundServiceType.FOREGROUND_SERVICE_TYPE_DATA_SYNC],
      ongoing: true,
      colorized: false,
    },
  });
}

export async function stopMonitoring(): Promise<void> {
  stopped = true;
  if (reconnectTimer) clearTimeout(reconnectTimer);
  socket?.close();
  socket = null;
  await notifee.stopForegroundService();
}
