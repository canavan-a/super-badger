// Mirrors ../horus-33/mobile/src/service/PresenceService.ts's shape 1:1,
// adapted to super-badger's single aggregate notifications feed (see
// server/api/ws.go's notificationsWS) instead of horus-33's presence socket.
//
// Only ever imported from native-only code paths: app/index.js (top-level,
// registers the foreground-service task), and dynamic import() from
// App.tsx/SettingsScreen.tsx (both shared with the Vite web build, which
// must never statically import notifee or this file).
import notifee, {AndroidForegroundServiceType} from '@notifee/react-native';

import {handleNotificationMsg, notificationColor} from '../notifications/agentEvents';
import {CH_SERVICE, ensureChannels, NID_FGS, PREFER_SMALL_ICON, PRESS_ACTION} from '../notifications/channels';
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

// Same half-open-socket watchdog as useStationChat's SOCKET_SILENCE_MS: the
// server heartbeats this feed every 60s (server/api/ws.go's
// wsNotificationsHeartbeat — slower than the chat socket's, to spare the
// cellular radio), so this long without a frame means the socket died
// without a close event (common across Android network changes and doze)
// and would otherwise sit there forever delivering nothing.
const SOCKET_SILENCE_MS = 150_000;
let lastFrameAt = 0;
let sawHeartbeat = false;
let watchdog: ReturnType<typeof setInterval> | null = null;

function connect(): void {
  if (stopped) return;
  const ws = new WebSocket(notificationsWsURL());
  socket = ws;
  sawHeartbeat = false;
  lastFrameAt = Date.now();

  ws.onopen = () => {
    backoffMs = 1000;
    lastFrameAt = Date.now();
  };
  ws.onmessage = ev => {
    if (socket !== ws) return;
    lastFrameAt = Date.now();
    try {
      const msg = JSON.parse(ev.data);
      if (msg.type === 'heartbeat') {
        // Must never reach handleNotificationMsg, which clears the
        // station's chat notifications before even looking at the type.
        sawHeartbeat = true;
        return;
      }
      handleNotificationMsg(msg).catch(err => {
        // Fire-and-forget from a WS message handler — without this, a
        // rejection here (e.g. Notifee misbehaving) vanished with zero
        // trace, no different from the notification just never showing up.
        console.warn('[BackgroundMonitorService] handleNotificationMsg failed', err);
      });
    } catch {
      // ignore malformed frames
    }
  };
  ws.onclose = () => {
    if (stopped || socket !== ws) return;
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
    if (watchdog) clearInterval(watchdog);
    watchdog = setInterval(() => {
      const ws = socket;
      if (stopped || ws?.readyState !== WebSocket.OPEN) return;
      if (sawHeartbeat && Date.now() - lastFrameAt > SOCKET_SILENCE_MS) {
        // Detach first so its late onclose doesn't schedule a second
        // reconnect alongside this one.
        socket = null;
        ws.close();
        if (reconnectTimer) clearTimeout(reconnectTimer);
        connect();
      }
    }, 10_000);
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
    data: PREFER_SMALL_ICON,
    android: {
      channelId: CH_SERVICE,
      // Required: Notifee's default is 'ic_launcher', which doesn't exist
      // (launcher icons are per-theme, ic_launcher_<theme>), and Android
      // kills the process over an icon-less foreground notification.
      smallIcon: 'ic_notification',
      pressAction: PRESS_ACTION,
      asForegroundService: true,
      color: await notificationColor(),
      foregroundServiceTypes: [AndroidForegroundServiceType.FOREGROUND_SERVICE_TYPE_DATA_SYNC],
      ongoing: true,
      colorized: false,
    },
  });
}

export async function stopMonitoring(): Promise<void> {
  stopped = true;
  if (reconnectTimer) clearTimeout(reconnectTimer);
  if (watchdog) clearInterval(watchdog);
  watchdog = null;
  socket?.close();
  socket = null;
  await notifee.stopForegroundService();
}
