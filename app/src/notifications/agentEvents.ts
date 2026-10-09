// Fires the alert notifications the background monitor cares about —
// see server/api/ws.go's notificationsWS for the message shapes this
// consumes. Deterministic per-station-per-type notification IDs mean a
// repeat event replaces the previous notification instead of stacking; no
// edge-detection needed since these are discrete one-shot server-pushed
// events, not a polled boolean state (contrast with horus-33's presence
// alerts, which do need edge-detection).
import notifee from '@notifee/react-native';

import {formatDataPointValue} from '../api';
import {notificationIconUri} from '../appIcon';
import {rgbToHex, themeAccent} from '../logo/logoTheme';
import {settingsStore} from '../settings';
import {THEMES} from '../theme';
import {CH_ALERTS, PRESS_ACTION} from './channels';

export interface NotificationMsg {
  type: 'agent_idle' | 'permission_requested' | 'question_requested' | 'datapoint_threshold';
  station_id: number;
  station_name: string;
  station_color?: string;
  key?: string;
  // Owner-supplied display name for `key` (see StationSettingsScreen) —
  // empty when never set, in which case the raw key is used instead.
  label?: string;
  decimals?: number;
  value?: number;
  direction?: 'above' | 'below';
}

// Every notification is one of two kinds, tagged via `data.kind` so a later
// notification's clearing pass can tell them apart (see below):
//  - "chat": agent_idle / permission_requested / question_requested —
//    transient "come look at the conversation" pings, fine to replace with
//    whatever's newest.
//  - "data": datapoint_threshold — a temperature/usage/etc. threshold that
//    tripped. These must never be silently cleared by a chat notification:
//    the owner needs to still see it went above/below threshold even if the
//    agent also went idle a moment later, so nothing but a fresh reading for
//    that same key (which reuses its id and replaces itself) touches it.
type NotificationKind = 'chat' | 'data';

async function clearChatNotifications(stationId: number): Promise<void> {
  const stationTag = String(stationId);
  const displayed = await notifee.getDisplayedNotifications();
  await Promise.all(
    displayed
      .filter(d => d.notification.data?.stationId === stationTag && d.notification.data?.kind === 'chat')
      .map(d => notifee.cancelNotification(d.notification.id!)),
  );
}

// The tint for the status-bar icon and the notification's accent: the
// station's own color when it has one, else the app theme's accent — the
// same color the themed launcher icon's S is drawn in.
export async function notificationColor(stationColor?: string): Promise<string> {
  if (stationColor) return stationColor;
  const {themeName} = await settingsStore.load();
  return rgbToHex(themeAccent(THEMES[themeName] ?? THEMES.light));
}

// The themed launcher icon as the notification's large icon. The tint above
// only reaches the monochrome status-bar S; the full-color app icon a
// notification shows is the manifest's static <application android:icon>,
// always the light one, since Android can't change that at runtime. Spread
// into the android options — empty (no large icon) when it couldn't be made,
// as Notifee rejects an explicit undefined.
export async function notificationLargeIcon(): Promise<{largeIcon?: string; circularLargeIcon?: boolean}> {
  const uri = await notificationIconUri();
  return uri ? {largeIcon: uri, circularLargeIcon: true} : {};
}

// The station whose chat is on screen with the app in the foreground, or
// null. Set by App.tsx. Its notifications are cleared the moment it's opened
// (that *is* "coming to look"), and new chat pings for it are skipped — no
// point buzzing about a conversation the owner is already watching.
let viewingStation: number | null = null;

export async function setViewingStation(stationId: number | null): Promise<void> {
  viewingStation = stationId;
  if (stationId !== null) await clearStationNotifications(stationId);
}

// Opening a station acknowledges everything it raised, threshold alerts
// included — its datapoints are right there in the header.
async function clearStationNotifications(stationId: number): Promise<void> {
  const stationTag = String(stationId);
  const displayed = await notifee.getDisplayedNotifications();
  await Promise.all(
    displayed
      .filter(d => d.notification.data?.stationId === stationTag)
      .map(d => notifee.cancelNotification(d.notification.id!)),
  );
}

export async function handleNotificationMsg(msg: NotificationMsg): Promise<void> {
  if (msg.station_id === viewingStation && msg.type !== 'datapoint_threshold') return;

  // Both kinds clear prior *chat* notifications for the station ("a data
  // notification overrides chat, but chat never clears data" — see above);
  // neither kind ever cancels an existing data notification.
  //
  // Best-effort: this used to be a plain `await` with nothing catching a
  // rejection, and the caller (BackgroundMonitorService's onmessage) invokes
  // this fire-and-forget with no .catch() either — so any failure here (a
  // stale/bad notification id, a Notifee call misbehaving from the
  // foreground-service JS context, etc.) silently aborted the whole function
  // *before* the switch below ever got to call displayNotification. That
  // turned "can't clean up an old notification" into "no notification ever
  // shows again," for every message type, which is what actually happened
  // here — never let housekeeping block the notification it's guarding.
  try {
    await clearChatNotifications(msg.station_id);
  } catch (err) {
    console.warn('[agentEvents] clearChatNotifications failed, continuing anyway', err);
  }

  const data = (kind: NotificationKind) => ({stationId: String(msg.station_id), kind});
  const color = await notificationColor(msg.station_color);
  const largeIcon = await notificationLargeIcon();

  switch (msg.type) {
    case 'agent_idle':
      await notifee.displayNotification({
        id: `agent-idle-${msg.station_id}`,
        title: 'Agent idle',
        body: `${msg.station_name} is waiting for your input`,
        data: data('chat'),
        android: {
          channelId: CH_ALERTS,
          smallIcon: 'ic_notification',
          pressAction: PRESS_ACTION,
          timestamp: Date.now(),
          showTimestamp: true,
          color,
          ...largeIcon,
        },
      });
      return;
    case 'permission_requested':
      await notifee.displayNotification({
        id: `permission-${msg.station_id}`,
        title: 'Permission requested',
        body: `${msg.station_name} needs a permission decision`,
        data: data('chat'),
        android: {
          channelId: CH_ALERTS,
          smallIcon: 'ic_notification',
          pressAction: PRESS_ACTION,
          timestamp: Date.now(),
          showTimestamp: true,
          color,
          ...largeIcon,
        },
      });
      return;
    case 'question_requested':
      await notifee.displayNotification({
        id: `question-${msg.station_id}`,
        title: 'Question asked',
        body: `${msg.station_name} needs an answer`,
        data: data('chat'),
        android: {
          channelId: CH_ALERTS,
          smallIcon: 'ic_notification',
          pressAction: PRESS_ACTION,
          timestamp: Date.now(),
          showTimestamp: true,
          color,
          ...largeIcon,
        },
      });
      return;
    case 'datapoint_threshold': {
      const display = msg.label || msg.key;
      const value = msg.value !== undefined ? formatDataPointValue(msg.value, msg.decimals ?? 1) : msg.value;
      await notifee.displayNotification({
        id: `datapoint-${msg.station_id}-${msg.key}`,
        title: `${msg.station_name}: ${display} ${msg.direction} threshold`,
        body: `${display} is now ${value}`,
        data: data('data'),
        android: {
          channelId: CH_ALERTS,
          smallIcon: 'ic_notification',
          pressAction: PRESS_ACTION,
          timestamp: Date.now(),
          showTimestamp: true,
          color,
          ...largeIcon,
        },
      });
      return;
    }
    default:
    // Unknown message type — ignore rather than throw, this socket is
    // meant to tolerate the server adding new message types over time.
  }
}
