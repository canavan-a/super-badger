/**
 * @format
 */

import notifee, {EventType} from '@notifee/react-native';
import {AppRegistry} from 'react-native';
import App from './src/App';
import {name as appName} from './app.json';
import {pendingNav} from './src/notifications/pendingNav';
import {backgroundRunner} from './src/service/BackgroundMonitorService';

// Registers the function Notifee's foreground service actually runs (see
// BackgroundMonitorService.backgroundRunner's doc comment) — must happen at
// module top level, before AppRegistry.registerComponent, mirroring
// ../horus-33/mobile/index.js exactly.
notifee.registerForegroundService(() => backgroundRunner());

// Handles a notification tap while the app is alive but backgrounded (not
// killed) — this file's JS context stays alive in that case, but there's no
// mounted React tree to call navigate() on directly, so the target station
// id is stashed in pendingNav for App.tsx to pick up on its next mount/tick.
// (A tap while the app is fully killed instead goes through
// notifee.getInitialNotification() in App.tsx's startup effect; a tap while
// the app is already in the foreground fires notifee.onForegroundEvent,
// registered inside App.tsx directly.)
notifee.onBackgroundEvent(async ({type, detail}) => {
  if (type === EventType.PRESS) {
    const id = detail.notification?.data?.stationId;
    if (id) pendingNav.set(Number(id));
    // Notifee tears down this headless JS task as soon as the returned
    // promise resolves, so an un-awaited cancel here can be killed before it
    // actually lands — this handler being backgrounded (not killed) is
    // exactly the case this cancel matters most for.
    if (detail.notification?.id) await notifee.cancelNotification(detail.notification.id);
  }
});

AppRegistry.registerComponent(appName, () => App);
