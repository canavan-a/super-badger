// Notifee channel setup — mirrors ../horus-33/mobile/src/notifications/channels.ts.
// Only ever imported from native-only code paths (BackgroundMonitorService,
// index.js, or dynamic imports from shared screens) — never statically
// imported from a file the Vite web build bundles.
import notifee, {AndroidImportance} from '@notifee/react-native';

export const CH_SERVICE = 'superbadger-fgs';
export const CH_ALERTS = 'superbadger-alerts';
export const NID_FGS = 'superbadger-monitoring';

let ensured = false;

export async function ensureChannels(): Promise<void> {
  if (ensured) return;
  ensured = true;
  await notifee.createChannel({id: CH_SERVICE, name: 'Monitoring', importance: AndroidImportance.LOW});
  await notifee.createChannel({id: CH_ALERTS, name: 'Agent alerts', importance: AndroidImportance.HIGH});
}
