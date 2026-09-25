import AsyncStorage from '@react-native-async-storage/async-storage';
import {Platform} from 'react-native';

// Android emulators reach the host machine via 10.0.2.2, not localhost — the
// standard emulator-to-host loopback alias. Overridable at runtime from the
// Settings screen (e.g. for a physical device pointed at a LAN IP).
const DEFAULT_WEB_URL = 'http://localhost:8080';
const DEFAULT_ANDROID_URL = 'http://10.0.2.2:8080';
const DEFAULT_URL = Platform.OS === 'web' ? DEFAULT_WEB_URL : DEFAULT_ANDROID_URL;

const STORAGE_KEY = 'superbadger.settings.v1';

export interface Settings {
  serverUrl: string;
  authToken: string;
  // Kept here (rather than a separate store) so the existing load/save/
  // subscribe plumbing — and its single AsyncStorage key — covers theme
  // persistence for free. See src/theme.ts for the actual palettes.
  themeName: 'light' | 'dark' | 'slate' | 'sepia' | 'ember';
  // Android-only: keeps a background WebSocket + foreground service alive
  // (see src/service/BackgroundMonitorService.ts) to push a notification
  // when an agent goes idle or a permission is requested, even while the
  // app isn't open. Off by default — it's a real foreground service with a
  // persistent notification, opt-in only.
  bgNotifications: boolean;
  // Android-only: switch the launcher icon to the one matching the current
  // theme (see src/appIcon.ts). On by default; turn it off if your launcher
  // reacts badly to icons changing.
  themedIcon: boolean;
  // Play the animated launch splash. On by default; switching it off skips the
  // splash entirely (also a quick way to rule it out when debugging a crash).
  launchSplash: boolean;
  // Crash-loop protection for the splash: a launch counts as a strike before
  // the animation starts and is cleared when it finishes, so a launch that dies
  // mid-animation leaves one behind. After MAX_SPLASH_STRIKES in a row the
  // splash turns itself off (and says so in Settings) instead of crashing again.
  splashStrikes: number;
  splashAutoDisabled: boolean;
}

const DEFAULT_SETTINGS: Settings = {
  serverUrl: DEFAULT_URL,
  authToken: '',
  themeName: 'light',
  bgNotifications: false,
  themedIcon: true,
  launchSplash: true,
  splashStrikes: 0,
  splashAutoDisabled: false,
};

type Listener = (settings: Settings) => void;

let current: Settings = DEFAULT_SETTINGS;
let loaded = false;
const listeners = new Set<Listener>();

async function load(): Promise<Settings> {
  if (loaded) {
    return current;
  }
  try {
    const raw = await AsyncStorage.getItem(STORAGE_KEY);
    if (raw) {
      current = {...DEFAULT_SETTINGS, ...JSON.parse(raw)};
    }
  } catch {
    // fall back to defaults — a corrupt/missing value shouldn't crash startup
  }
  loaded = true;
  return current;
}

async function save(next: Settings): Promise<void> {
  current = next;
  listeners.forEach(l => l(current));
  try {
    await AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(next));
  } catch {
    // best-effort persistence; the in-memory value above is still updated
  }
}

export const settingsStore = {
  get: (): Settings => current,
  load,
  save,
  subscribe(listener: Listener): () => void {
    listeners.add(listener);
    return () => listeners.delete(listener);
  },
};
