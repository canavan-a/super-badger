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

// Tap target for every notification. Not Notifee's 'default' launch activity:
// that resolves to whichever launcher alias is enabled (.IconLight, .IconDark,
// … — see AndroidManifest.xml), which isn't a class Notifee can start, so taps
// did nothing. Name the real activity instead.
export const PRESS_ACTION = {id: 'default', launchActivity: 'com.superbadgerapp.MainActivity'};

// Spread into every notification's `data`. From Android 17 (API 37) the card
// shows the app's launcher icon instead of the small icon, and for us that's
// the light theme's, whatever theme is set (Android can't change the app icon
// at runtime). This is Notification.EXTRA_PREFER_SMALL_ICON, asking for the
// small icon instead: the S, tinted with the notification's color. Older
// Android ignores it. Notifee copies `data` into the notification's extras as
// is, but Android reads this one as a boolean, which Notifee's types and
// validator only allow strings/numbers/objects for; patches/ relaxes the
// validator, and the cast covers the types.
export const PREFER_SMALL_ICON = {'android.app.preferSmallIcon': true as unknown as string};
