// Fires the two alert notifications the background monitor cares about —
// see server/api/ws.go's notificationsWS for the message shapes this
// consumes. Deterministic per-station-per-type notification IDs mean a
// repeat event replaces the previous notification instead of stacking; no
// edge-detection needed since these are discrete one-shot server-pushed
// events, not a polled boolean state (contrast with horus-33's presence
// alerts, which do need edge-detection).
import notifee from '@notifee/react-native';

import {CH_ALERTS} from './channels';

export interface NotificationMsg {
  type: 'agent_idle' | 'permission_requested' | 'datapoint_threshold';
  station_id: number;
  station_name: string;
  key?: string;
  value?: number;
  direction?: 'above' | 'below';
}

export async function handleNotificationMsg(msg: NotificationMsg): Promise<void> {
  switch (msg.type) {
    case 'agent_idle':
      await notifee.displayNotification({
        id: `agent-idle-${msg.station_id}`,
        title: 'Agent idle',
        body: `${msg.station_name} is waiting for your input`,
        data: {stationId: String(msg.station_id)},
        android: {
          channelId: CH_ALERTS,
          smallIcon: 'ic_notification',
          pressAction: {id: 'default'},
          timestamp: Date.now(),
          showTimestamp: true,
        },
      });
      return;
    case 'permission_requested':
      await notifee.displayNotification({
        id: `permission-${msg.station_id}`,
        title: 'Permission requested',
        body: `${msg.station_name} needs a permission decision`,
        data: {stationId: String(msg.station_id)},
        android: {
          channelId: CH_ALERTS,
          smallIcon: 'ic_notification',
          pressAction: {id: 'default'},
          timestamp: Date.now(),
          showTimestamp: true,
        },
      });
      return;
    case 'datapoint_threshold':
      await notifee.displayNotification({
        id: `datapoint-${msg.station_id}-${msg.key}`,
        title: `${msg.station_name}: ${msg.key} ${msg.direction} threshold`,
        body: `${msg.key} is now ${msg.value}`,
        data: {stationId: String(msg.station_id)},
        android: {
          channelId: CH_ALERTS,
          smallIcon: 'ic_notification',
          pressAction: {id: 'default'},
          timestamp: Date.now(),
          showTimestamp: true,
        },
      });
      return;
    default:
    // Unknown message type — ignore rather than throw, this socket is
    // meant to tolerate the server adding new message types over time.
  }
}
