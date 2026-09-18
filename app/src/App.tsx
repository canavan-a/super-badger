import React, {useCallback, useEffect, useRef, useState} from 'react';
import {Platform, Pressable, SafeAreaView, StatusBar, StyleSheet, Text, View} from 'react-native';

import {checkServerHealth, listStations, Station} from './api';
import {Drawer} from './components/Drawer';
import {Icon, IconName} from './components/Icon';
import {StationsDrawerContent} from './components/StationsDrawerContent';
import {pendingNav} from './notifications/pendingNav';
import {Route} from './routes';
import {AddStationScreen} from './screens/AddStationScreen';
import {SettingsScreen} from './screens/SettingsScreen';
import {StationDetailScreen} from './screens/StationDetailScreen';
import {StationSettingsScreen} from './screens/StationSettingsScreen';
import {StationsHomeScreen} from './screens/StationsHomeScreen';
import {settingsStore} from './settings';
import {Theme, ThemeProvider, useTheme} from './theme';

const TITLES: Record<Route['name'], string> = {
  stations: 'super-badger',
  stationDetail: 'Station',
  stationSettings: 'Data Points',
  addStation: 'Add Station',
  settings: 'Settings',
};

// StationDetailScreen reports its own title/subtitle/actions up here so they
// render directly in the one top bar instead of a second header block
// stacked below it — the app only has one screen at a time, so it only ever
// needs one header.
export interface HeaderInfo {
  title: string;
  // Small colored dot rendered right next to the title (e.g. a station's
  // live active/idle/unreachable state) — a glance-able signal that doesn't
  // need reading, unlike the old "· active" text buried in the subtitle.
  indicator?: {color: string; label: string};
  subtitle?: string;
  // Small labeled pills rendered below the subtitle — one per owner-surfaced
  // data point (see StationSettingsScreen's "Show on top bar" toggle),
  // reading "Label: value" instead of being folded into the subtitle string
  // as plain text.
  badges?: {label: string; value: string}[];
  // Plain muted number shown inline right after the title, in a larger font
  // (e.g. the opted-in token count) — unlike `badges`, this has no label or
  // pill styling, just the bare value.
  tokenCount?: string;
  // `icon`, when set, renders as a compact icon-only button using `label`
  // only for its accessibility label — saves header space for actions that
  // don't need to read as text (see StationDetailScreen's "Data" action,
  // which opens the top-bar/data config).
  actions?: {label: string; onPress: () => void; destructive?: boolean; icon?: IconName}[];
}

function AppInner(): React.JSX.Element {
  const theme = useTheme();
  const [route, setRoute] = useState<Route>({name: 'stations'});
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [stations, setStations] = useState<Station[]>([]);
  const [loading, setLoading] = useState(true);
  // null = "haven't checked yet" — deliberately distinct from false, so the
  // banner doesn't flash on for an instant on every fresh load.
  const [serverOnline, setServerOnline] = useState<boolean | null>(null);
  const [headerInfo, setHeaderInfo] = useState<HeaderInfo | null>(null);

  const refreshStations = useCallback(() => {
    setLoading(true);
    listStations()
      .then(setStations)
      .catch(() => setStations([]))
      .finally(() => setLoading(false));
  }, []);

  // Settings (including a possibly-customized server URL) load
  // asynchronously from storage. The route-change effect below also wants to
  // refresh on the initial route, and a refresh fired before settings finish
  // loading can resolve *after* the settings-aware one — since it's hitting
  // a stale/wrong URL, its failure can take longer than the correct fetch's
  // success, so it lands second and clobbers good data with an empty list.
  // This ref gates that effect until settings are actually ready.
  const settingsReady = useRef(false);

  useEffect(() => {
    settingsStore.load().then(() => {
      settingsReady.current = true;
      refreshStations();
    });
  }, [refreshStations]);

  // Station list changes (create/delete) happen from other screens; a fresh
  // fetch whenever we land back on a route that shows the list keeps the
  // drawer in sync without a shared store.
  useEffect(() => {
    if (!settingsReady.current) return;
    if (route.name === 'stations' || route.name === 'stationDetail') {
      refreshStations();
    }
  }, [route, refreshStations]);

  // Polls superbadger's own health, independent of anything station-related
  // — a wrong/unreachable server address in Settings, or the server process
  // just not running, should be obvious immediately rather than looking
  // like "no stations" or a hung request.
  useEffect(() => {
    let cancelled = false;
    const check = () => {
      checkServerHealth().then(ok => {
        if (!cancelled) setServerOnline(ok);
      });
    };
    check();
    const interval = setInterval(check, 10000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, []);

  const navigate = (next: Route) => {
    if (next.name !== 'stationDetail' && next.name !== 'stationSettings') {
      setHeaderInfo(null);
    }
    setRoute(next);
    setDrawerOpen(false);
  };

  const openStation = useCallback((stationId: number) => {
    navigate({name: 'stationDetail', id: stationId});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Background notifications (see src/service/BackgroundMonitorService.ts):
  // resume monitoring on launch if it was previously enabled — without this
  // the user would have to re-flip the Settings toggle every time the app
  // is fully relaunched. Dynamic import so the web bundle never has to
  // resolve notifee.
  useEffect(() => {
    if (Platform.OS !== 'android') return;
    settingsStore.load().then(s => {
      if (!s.bgNotifications) return;
      import('./service/BackgroundMonitorService').then(({startMonitoring}) => {
        startMonitoring().catch(() => {
          // Best-effort resume; the Settings toggle still reflects the
          // stored preference and can be used to retry.
        });
      });
    });
  }, []);

  // Notification tap -> open that station, covering all three states a tap
  // can happen in:
  //  - app fully killed: notifee.getInitialNotification() on this cold start
  //  - app alive but backgrounded: index.js's onBackgroundEvent stashed the
  //    target in pendingNav (no React tree to navigate() from there)
  //  - app already in the foreground: notifee.onForegroundEvent, registered
  //    directly here
  useEffect(() => {
    if (Platform.OS !== 'android') return;
    let unsubscribeForeground: (() => void) | undefined;

    import('@notifee/react-native').then(({default: notifee, EventType}) => {
      notifee.getInitialNotification().then(initial => {
        const id = initial?.notification.data?.stationId;
        if (id) openStation(Number(id));
      });

      unsubscribeForeground = notifee.onForegroundEvent(({type, detail}) => {
        if (type === EventType.PRESS) {
          const id = detail.notification?.data?.stationId;
          if (id) openStation(Number(id));
        }
      });
    });

    const unsubscribePending = pendingNav.subscribe(stationId => {
      pendingNav.clear();
      openStation(stationId);
    });
    const already = pendingNav.get();
    if (already !== null) {
      pendingNav.clear();
      openStation(already);
    }

    return () => {
      unsubscribeForeground?.();
      unsubscribePending();
    };
  }, [openStation]);

  const showingDetailHeader = (route.name === 'stationDetail' || route.name === 'stationSettings') && headerInfo;
  const styles = makeStyles(theme);

  return (
    <SafeAreaView style={styles.container}>
      <StatusBar barStyle={theme.name === 'light' || theme.name === 'sepia' ? 'dark-content' : 'light-content'} />

      {serverOnline === false && (
        <View style={styles.offlineBanner}>
          <Text style={styles.offlineBannerText}>
            Can't reach superbadger server — check it's running and the address in Settings.
          </Text>
        </View>
      )}

      <View style={styles.header}>
        <Pressable
          style={styles.hamburger}
          onPress={() => {
            // Refetch every time the menu opens rather than trusting
            // whatever fetch happened at mount/navigation time — cheap, and
            // it directly matches what the user expects ("open the menu,
            // see current data") instead of depending on load-order timing.
            refreshStations();
            setDrawerOpen(true);
          }}>
          <Icon name="menu" size={20} color={theme.text} />
        </Pressable>

        <View style={styles.headerTextArea}>
          <View style={styles.headerTitleRow}>
            <Text style={styles.headerTitle} numberOfLines={1}>
              {showingDetailHeader ? headerInfo!.title : TITLES[route.name]}
            </Text>
            {showingDetailHeader && headerInfo!.indicator ? (
              <View
                style={[styles.headerIndicatorDot, {backgroundColor: headerInfo!.indicator!.color}]}
                accessibilityLabel={headerInfo!.indicator!.label}
              />
            ) : null}
            {showingDetailHeader && headerInfo!.tokenCount ? (
              <Text style={styles.headerTokenCount}>{headerInfo!.tokenCount}</Text>
            ) : null}
          </View>
          {showingDetailHeader && headerInfo!.subtitle ? (
            <Text style={styles.headerSubtitle} numberOfLines={1}>
              {headerInfo!.subtitle}
            </Text>
          ) : null}
          {showingDetailHeader && headerInfo!.badges && headerInfo!.badges!.length > 0 ? (
            <View style={styles.headerBadgeRow}>
              {headerInfo!.badges!.map(b => (
                <View key={b.label} style={styles.headerBadge}>
                  <Text style={styles.headerBadgeLabel}>{b.label}</Text>
                  <Text style={styles.headerBadgeValue}>{b.value}</Text>
                </View>
              ))}
            </View>
          ) : null}
        </View>

        {showingDetailHeader && headerInfo!.actions ? (
          <View style={styles.headerActions}>
            {headerInfo!.actions.map(action =>
              action.icon ? (
                <Pressable
                  key={action.label}
                  style={styles.headerIconButton}
                  onPress={action.onPress}
                  accessibilityLabel={action.label}>
                  <Icon name={action.icon} size={19} color={theme.text} />
                </Pressable>
              ) : (
                <Pressable key={action.label} style={styles.headerActionButton} onPress={action.onPress}>
                  <Text
                    style={[
                      styles.headerActionText,
                      action.destructive && styles.headerActionDestructive,
                    ]}>
                    {action.label}
                  </Text>
                </Pressable>
              ),
            )}
          </View>
        ) : null}
      </View>

      <View style={styles.body}>
        {route.name === 'stations' && (
          <StationsHomeScreen stations={stations} loading={loading} onNavigate={navigate} />
        )}
        {route.name === 'stationDetail' && (
          // Keyed by stationId so navigating from one station's chat straight
          // to another's fully remounts the screen instead of reusing the
          // same component instance — without this, useStationChat's state
          // (built up from the first station's history/WS events) carried
          // over into the second station's chat, showing its transcript.
          <StationDetailScreen
            key={route.id}
            stationId={route.id}
            onNavigate={navigate}
            onHeaderChange={setHeaderInfo}
          />
        )}
        {route.name === 'stationSettings' && (
          <StationSettingsScreen
            key={route.id}
            stationId={route.id}
            onHeaderChange={setHeaderInfo}
            onNavigate={navigate}
          />
        )}
        {route.name === 'addStation' && <AddStationScreen onNavigate={navigate} />}
        {route.name === 'settings' && <SettingsScreen />}
      </View>

      <Drawer open={drawerOpen} onClose={() => setDrawerOpen(false)}>
        <StationsDrawerContent stations={stations} loading={loading} onNavigate={navigate} />
      </Drawer>
    </SafeAreaView>
  );
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    container: {
      flex: 1,
      backgroundColor: theme.bg,
    },
    offlineBanner: {
      backgroundColor: theme.dangerBg,
      paddingVertical: 8,
      paddingHorizontal: 16,
      borderBottomWidth: 1,
      borderBottomColor: theme.danger,
    },
    offlineBannerText: {
      color: theme.danger,
      fontSize: 12,
      fontWeight: '600',
      textAlign: 'center',
    },
    header: {
      flexDirection: 'row',
      // flex-start (not center): centering against a cross-axis height set
      // by whichever column is tallest (the title/badges column, once
      // badges wrap to a second line) left the hamburger and action buttons
      // vertically centered in that taller row - i.e. visibly padded below
      // them relative to the title's top edge. Top-aligning keeps every
      // column starting at the same line regardless of how tall the badge
      // row grows.
      alignItems: 'flex-start',
      paddingHorizontal: 12,
      paddingVertical: 12,
      borderBottomWidth: 1,
      borderBottomColor: theme.border,
    },
    hamburger: {
      width: 36,
      height: 36,
      alignItems: 'center',
      justifyContent: 'center',
      marginRight: 4,
    },
    headerTextArea: {
      flex: 1,
      minWidth: 0,
    },
    headerTitleRow: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 6,
    },
    headerTitle: {
      fontSize: 16,
      fontWeight: '700',
      color: theme.text,
      flexShrink: 1,
    },
    headerIndicatorDot: {
      width: 8,
      height: 8,
      borderRadius: 4,
    },
    headerTokenCount: {
      fontSize: 20,
      fontWeight: '700',
      color: theme.textMuted,
    },
    headerSubtitle: {
      fontSize: 11,
      color: theme.textMuted,
      marginTop: 1,
    },
    headerBadgeRow: {
      flexDirection: 'row',
      flexWrap: 'wrap',
      justifyContent: 'flex-start',
      alignSelf: 'flex-start',
      gap: 4,
      marginTop: 4,
    },
    headerBadge: {
      flexDirection: 'row',
      alignItems: 'baseline',
      gap: 3,
      paddingHorizontal: 6,
      paddingVertical: 2,
      borderRadius: 8,
      backgroundColor: theme.surfaceAlt,
    },
    headerBadgeLabel: {
      fontSize: 10,
      fontWeight: '600',
      color: theme.textMuted,
    },
    headerBadgeValue: {
      fontSize: 10,
      fontWeight: '700',
      color: theme.text,
    },
    headerActions: {
      flexDirection: 'row',
      gap: 6,
    },
    headerActionButton: {
      paddingHorizontal: 10,
      paddingVertical: 6,
      borderRadius: 6,
      backgroundColor: theme.surfaceAlt,
    },
    headerActionText: {
      fontSize: 12,
      fontWeight: '600',
      color: theme.text,
    },
    headerActionDestructive: {
      color: theme.danger,
    },
    headerIconButton: {
      width: 32,
      height: 32,
      alignItems: 'center',
      justifyContent: 'center',
    },
    body: {
      flex: 1,
    },
  });
}

function App(): React.JSX.Element {
  return (
    <ThemeProvider>
      <AppInner />
    </ThemeProvider>
  );
}

export default App;
