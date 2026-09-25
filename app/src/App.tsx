import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {
  Animated,
  Dimensions,
  PanResponder,
  Platform,
  Pressable,
  SafeAreaView,
  StatusBar,
  StyleSheet,
  Text,
  View,
} from 'react-native';

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
import {SplashGate} from './logo/SplashGate';
import {useThemedAppIcon} from './appIcon';

const TITLES: Record<Route['name'], string> = {
  stations: 'Super Badger',
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
  // The station's own chosen color (see StationSettingsScreen's color
  // picker) — distinct from `indicator`, which is reachability/status, not
  // identity. Rendered as a left-edge accent stripe on the header bar,
  // matching the same station color shown as a left-border accent on
  // StationCard (station list) and the drawer's station rows.
  accentColor?: string;
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
  // Keeps the launcher icon in step with the theme (Android; see appIcon.ts).
  useThemedAppIcon();
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

  // useCallback (stable identity) matters here: it's passed down as
  // `onNavigate` and used as an effect dependency in child screens (e.g.
  // StationSettingsScreen's header-back-button effect) — a fresh function
  // reference every render would retrigger those effects every render, and
  // since this effect's body itself calls a setState, that becomes an
  // infinite render loop ("Maximum update depth exceeded"), confirmed live
  // via adb logcat.
  const navigate = useCallback((next: Route) => {
    if (next.name !== 'stationDetail' && next.name !== 'stationSettings') {
      setHeaderInfo(null);
    }
    setRoute(next);
    setDrawerOpen(false);
  }, []);

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
        if (initial?.notification.id) notifee.cancelNotification(initial.notification.id);
      });

      unsubscribeForeground = notifee.onForegroundEvent(({type, detail}) => {
        if (type === EventType.PRESS) {
          const id = detail.notification?.data?.stationId;
          if (id) openStation(Number(id));
          if (detail.notification?.id) notifee.cancelNotification(detail.notification.id);
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

  // Edge-swipe station cycling: dragging in from the right edge of a
  // station's chat screen steps to the *previous* station in the list
  // (wrapping around at the start) — with 3 stations that's 3, 2, 1, 3, 2,
  // 1, ... starting from station 3. "Previous" (not "next") is what actually
  // matches a swipe that drags new content in from the right: the station
  // that slides into view from off the right edge is the one before the
  // current one in list order.
  //
  // Built on core PanResponder/Animated rather than adding
  // react-native-gesture-handler/reanimated — those need native
  // linking/rebuild, which is a lot of new surface for one gesture when the
  // built-in APIs already cover it.
  const orderedStations = useMemo(() => [...stations].sort((a, b) => a.id - b.id), [stations]);
  const currentStationIndex =
    route.name === 'stationDetail' ? orderedStations.findIndex(s => s.id === route.id) : -1;
  const screenWidth = Dimensions.get('window').width;
  const swipeX = useRef(new Animated.Value(0)).current;
  const EDGE_ZONE = 28;
  const SWIPE_THRESHOLD = 70;

  const canSwipe = route.name === 'stationDetail' && currentStationIndex >= 0 && orderedStations.length > 1;
  // Guards against a new edge-swipe being accepted while the previous one's
  // commit/snap-back animation is still running — without this, a second
  // gesture's onPanResponderMove calls swipeX.setValue() directly, which
  // fights the in-flight Animated.timing/spring and can leave swipeX stuck at
  // whatever partial value the two last collided on.
  const swipeAnimating = useRef(false);

  // Not guarded by swipeAnimating: this is just the under-threshold/torn-away
  // case, where nothing is fighting the spring for control of swipeX — a
  // new drag's onPanResponderMove can freely override it mid-spring the same
  // way it would any other in-progress Animated value. Blocking new gestures
  // for the spring's full multi-hundred-ms settle time (unlike the commit
  // path's fixed-duration timings, a bounciness spring doesn't have a fixed
  // one) made an immediate re-swipe right after a rejected one get silently
  // ignored.
  const snapBack = () => {
    Animated.spring(swipeX, {toValue: 0, useNativeDriver: true, bounciness: 6}).start();
  };

  const panResponder = useMemo(
    () =>
      PanResponder.create({
        onStartShouldSetPanResponder: evt =>
          canSwipe && !swipeAnimating.current && evt.nativeEvent.pageX > screenWidth - EDGE_ZONE,
        onMoveShouldSetPanResponder: (evt, gesture) =>
          canSwipe &&
          !swipeAnimating.current &&
          evt.nativeEvent.pageX > screenWidth - EDGE_ZONE &&
          gesture.dx < -5 &&
          // Without a directionality check, a vertical scroll starting in
          // this edge strip that drifts left as little as 5px claims this
          // responder instead of the chat FlatList underneath — and with
          // onPanResponderTerminationRequest below refusing to give it back,
          // that scroll gesture gets swallowed entirely. Requiring the drag
          // to be predominantly horizontal keeps normal vertical scrolling
          // (even one that starts near the edge) working.
          Math.abs(gesture.dx) > Math.abs(gesture.dy) * 2,
        onPanResponderMove: (_evt, gesture) => {
          // Only follow a leftward drag (new content coming in from the
          // right) — clamp so it can't be dragged the other way.
          swipeX.setValue(Math.min(0, gesture.dx));
        },
        onPanResponderRelease: (_evt, gesture) => {
          if (gesture.dx < -SWIPE_THRESHOLD) {
            swipeAnimating.current = true;
            Animated.timing(swipeX, {toValue: -screenWidth, duration: 180, useNativeDriver: true}).start(
              ({finished}) => {
                // A second gesture starting mid-animation is now blocked by
                // swipeAnimating above, but an interrupted timing (e.g. a
                // future programmatic swipeX change) still invokes this
                // callback with finished:false — skip the navigate/slide-in
                // in that case instead of running it against a swipeX value
                // that was never actually driven all the way to -screenWidth.
                if (!finished) {
                  swipeAnimating.current = false;
                  return;
                }
                const prevIndex = (currentStationIndex - 1 + orderedStations.length) % orderedStations.length;
                navigate({name: 'stationDetail', id: orderedStations[prevIndex].id});
                // Land the incoming screen just off the right edge, then
                // animate it sliding in to 0 — the "gradual" part of the
                // transition, rather than an instant cut to the new station.
                swipeX.setValue(screenWidth);
                Animated.timing(swipeX, {toValue: 0, duration: 220, useNativeDriver: true}).start(() => {
                  swipeAnimating.current = false;
                });
              },
            );
          } else {
            snapBack();
          }
        },
        // The gesture can be torn away mid-drag (another responder, e.g. a
        // ScrollView or the OS back-gesture edge, claiming it) — that fires
        // onPanResponderTerminate instead of onPanResponderRelease, and
        // without a handler here swipeX was left stuck at whatever partial
        // value the drag last set. Snap back the same way an
        // under-threshold release does.
        onPanResponderTerminate: () => {
          snapBack();
        },
        // Refusing termination requests keeps this gesture from being torn
        // away mid-drag in the first place (the above stays as a fallback
        // for the cases this can't prevent, e.g. the OS itself reclaiming
        // it).
        onPanResponderTerminationRequest: () => false,
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [canSwipe, currentStationIndex, orderedStations, screenWidth],
  );

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

      <View
        style={[
          styles.header,
          showingDetailHeader && styles.headerDetail,
          showingDetailHeader && headerInfo!.accentColor
            ? {borderLeftWidth: 4, borderLeftColor: headerInfo!.accentColor}
            : null,
        ]}>
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

      <Animated.View
        style={[styles.body, {transform: [{translateX: swipeX}]}]}
        {...panResponder.panHandlers}>
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
      </Animated.View>

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
      // Plain center alignment for a single-line title (every non-station
      // screen, and the station screens before their data loads) - the
      // hamburger/actions are the same height as the title line here, so
      // centering them together looks right.
      alignItems: 'center',
      paddingHorizontal: 12,
      paddingVertical: 12,
      borderBottomWidth: 1,
      borderBottomColor: theme.border,
    },
    // Once a station header's badges can wrap to a second line, centering
    // against that taller block left the hamburger/action buttons visibly
    // padded below the title's top edge. Top-aligning instead keeps every
    // column starting at the same line regardless of how tall the badge
    // row grows - only applied here, not on the plain single-line title
    // screens above, where top-aligning made the title look off-center
    // against the fixed-height hamburger/action icon boxes.
    headerDetail: {
      alignItems: 'flex-start',
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
      <SplashGate>
        <AppInner />
      </SplashGate>
    </ThemeProvider>
  );
}

export default App;
