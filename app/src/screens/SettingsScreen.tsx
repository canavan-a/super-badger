import React, {useEffect, useState} from 'react';
import {
  ActivityIndicator,
  Alert,
  Linking,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from 'react-native';

import {
  compareVersions,
  DowngradeBlockedError,
  fetchReleases,
  getVersionInfo,
  onDownloadProgress,
  Release,
  runUpdate,
} from '../appUpdater';
import {MetricSourcesSettings} from '../components/MetricSourcesSettings';
import {VpnSettings} from '../components/VpnSettings';
import {settingsStore} from '../settings';
import {THEME_OPTIONS, Theme, useThemeSetting} from '../theme';

type TestState = {status: 'idle'} | {status: 'testing'} | {status: 'ok'} | {status: 'error'; message: string};

export function SettingsScreen(): React.JSX.Element {
  const {theme, themeName, setThemeName} = useThemeSetting();
  const styles = makeStyles(theme);

  const [serverUrl, setServerUrl] = useState('');
  const [authToken, setAuthToken] = useState('');
  const [saved, setSaved] = useState(false);
  const [testState, setTestState] = useState<TestState>({status: 'idle'});

  // Android app upgrade flow — mirrors ../horus-33/mobile's SettingsScreen.
  // Nothing to check on web/iOS: web has no "app" to update at all (it's
  // just whatever's currently served), and this app has no iOS build.
  const [version, setVersion] = useState<string>();
  const [releases, setReleases] = useState<Release[]>([]);
  const [selectedTag, setSelectedTag] = useState<string>();
  const [updateStatus, setUpdateStatus] = useState<string>();
  const [updateBusy, setUpdateBusy] = useState(false);
  const [checkingUpdates, setCheckingUpdates] = useState(false);

  // Background notifications (agent-idle / permission-requested) — Android
  // only, mirrors ../horus-33/mobile's bgAlerts switch. Toggling dynamically
  // imports notifee/BackgroundMonitorService since this screen is shared
  // with the Vite web build, which must never statically import either.
  const [bgNotifications, setBgNotifications] = useState(false);
  const [bgNotifBusy, setBgNotifBusy] = useState(false);

  useEffect(() => {
    settingsStore.load().then(s => {
      setServerUrl(s.serverUrl);
      setAuthToken(s.authToken);
      setBgNotifications(s.bgNotifications);
      testConnection(s.serverUrl);
    });
    if (Platform.OS === 'android') {
      getVersionInfo()
        .then(v => setVersion(`${v.versionName} (${v.versionCode})`))
        .catch(() => setVersion('unknown'));
      checkForUpdates();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const toggleBgNotifications = async (value: boolean) => {
    setBgNotifBusy(true);
    try {
      const notifee = (await import('@notifee/react-native')).default;
      const {startMonitoring, stopMonitoring} = await import('../service/BackgroundMonitorService');
      if (value) {
        await notifee.requestPermission();
        await startMonitoring();
      } else {
        await stopMonitoring();
      }
      await settingsStore.save({...settingsStore.get(), bgNotifications: value});
      setBgNotifications(value);
    } catch (err) {
      Alert.alert('Background notifications', String(err));
    } finally {
      setBgNotifBusy(false);
    }
  };

  useEffect(() => {
    if (!updateBusy) return;
    const off = onDownloadProgress(({bytesDownloaded, bytesTotal}) => {
      const pct = bytesTotal > 0 ? Math.round((bytesDownloaded / bytesTotal) * 100) : 0;
      setUpdateStatus(`Downloading… ${pct}%`);
    });
    return off;
  }, [updateBusy]);

  const currentVersionName = version?.split(' ')[0];
  const latestRelease = releases[0];
  const updateAvailable =
    !!latestRelease && !!currentVersionName && compareVersions(latestRelease.version, currentVersionName) > 0;

  const checkForUpdates = async () => {
    setCheckingUpdates(true);
    setUpdateStatus(undefined);
    try {
      const rs = await fetchReleases();
      setReleases(rs);
      setSelectedTag(t => t ?? rs[0]?.tag);
    } catch (e) {
      setUpdateStatus(`Check failed: ${String(e)}`);
    } finally {
      setCheckingUpdates(false);
    }
  };

  const installUpdate = async () => {
    const target = releases.find(r => r.tag === selectedTag) ?? latestRelease;
    if (!target) return;
    if (currentVersionName && compareVersions(target.version, currentVersionName) < 0) {
      Alert.alert(
        'Downgrade blocked',
        `v${target.version} is older than the installed v${currentVersionName}. Android won't replace a newer build — uninstall the app first, then install the older version.`,
      );
      return;
    }
    setUpdateBusy(true);
    setUpdateStatus('Starting…');
    try {
      await runUpdate(target);
      setUpdateStatus('Opening installer…');
    } catch (e) {
      if (e instanceof DowngradeBlockedError) {
        Alert.alert('Downgrade blocked', 'Uninstall the app first to install an older version.');
        setUpdateStatus(undefined);
      } else {
        setUpdateStatus(`Update failed: ${String(e)}`);
      }
    } finally {
      setUpdateBusy(false);
    }
  };

  const testConnection = async (url: string) => {
    setTestState({status: 'testing'});
    try {
      const res = await fetch(`${url.replace(/\/+$/, '')}/health`);
      setTestState(res.ok ? {status: 'ok'} : {status: 'error', message: `status ${res.status}`});
    } catch (err) {
      setTestState({status: 'error', message: String(err)});
    }
  };

  const save = async () => {
    await settingsStore.save({...settingsStore.get(), serverUrl: serverUrl.trim(), authToken: authToken.trim()});
    setSaved(true);
    setTimeout(() => setSaved(false), 1500);
    testConnection(serverUrl.trim());
  };

  return (
    <ScrollView style={styles.scroll} contentContainerStyle={styles.container}>
      <Text style={styles.title}>Settings</Text>

      <Text style={styles.title2}>Appearance</Text>
      <View style={styles.themeRow}>
        {THEME_OPTIONS.map(opt => (
          <Pressable
            key={opt.name}
            style={[styles.themeSwatch, themeName === opt.name && styles.themeSwatchActive]}
            onPress={() => setThemeName(opt.name)}>
            <View style={styles.themeSwatchColors}>
              <View style={[styles.themeSwatchDot, {backgroundColor: THEME_PREVIEW[opt.name].bg}]} />
              <View style={[styles.themeSwatchDot, {backgroundColor: THEME_PREVIEW[opt.name].primary}]} />
            </View>
            <Text style={styles.themeSwatchLabel}>{opt.label}</Text>
          </Pressable>
        ))}
      </View>

      <View style={styles.divider} />

      <View style={styles.field}>
        <Text style={styles.label}>Server origin</Text>
        <TextInput
          style={styles.input}
          value={serverUrl}
          onChangeText={setServerUrl}
          placeholder="http://localhost:8080"
          placeholderTextColor={theme.textMuted}
          autoCapitalize="none"
          autoCorrect={false}
        />
        <Text style={styles.hint}>
          Base URL for the superbadger API. Android emulators can't reach
          "localhost" on the host — use 10.0.2.2 there, or the host's LAN IP
          for a physical device.
        </Text>

        <View style={styles.testRow}>
          <Pressable
            style={styles.testButton}
            onPress={() => testConnection(serverUrl)}
            disabled={testState.status === 'testing'}>
            {testState.status === 'testing' ? (
              <ActivityIndicator size="small" color={theme.text} />
            ) : (
              <Text style={styles.testButtonText}>Test Connection</Text>
            )}
          </Pressable>
          {testState.status === 'ok' && <Text style={styles.testOk}>✓ Reachable</Text>}
          {testState.status === 'error' && (
            <Text style={styles.testError} numberOfLines={1}>
              ✗ {testState.message}
            </Text>
          )}
        </View>
      </View>

      <View style={styles.field}>
        <Text style={styles.label}>Auth token</Text>
        <TextInput
          style={styles.input}
          value={authToken}
          onChangeText={setAuthToken}
          placeholder="(none yet — server has no auth)"
          placeholderTextColor={theme.textMuted}
          autoCapitalize="none"
          autoCorrect={false}
          secureTextEntry
        />
        <Text style={styles.hint}>
          Sent as "Authorization: Bearer …" once the server supports it — safe
          to leave blank for now.
        </Text>
      </View>

      <Pressable style={styles.button} onPress={save}>
        <Text style={styles.buttonText}>{saved ? 'Saved' : 'Save'}</Text>
      </Pressable>

      {Platform.OS === 'android' && (
        <>
          <View style={styles.divider} />
          <Text style={styles.title2}>App updates</Text>

          <Pressable onPress={checkForUpdates} disabled={checkingUpdates}>
            <Text style={styles.hint}>
              {checkingUpdates
                ? 'Checking…'
                : latestRelease
                ? updateAvailable
                  ? `Update available: v${latestRelease.version}`
                  : `Up to date (v${latestRelease.version})`
                : `Current version: ${version ?? '…'}`}
            </Text>
          </Pressable>

          {latestRelease && (
            <Pressable
              style={[styles.button, updateBusy && styles.buttonDisabled]}
              disabled={updateBusy}
              onPress={installUpdate}>
              <Text style={styles.buttonText}>
                {updateBusy ? 'Working…' : updateAvailable ? `Update to v${latestRelease.version}` : 'Reinstall current version'}
              </Text>
            </Pressable>
          )}

          {updateStatus && <Text style={styles.hint}>{updateStatus}</Text>}

          <View style={styles.divider} />
          <Text style={styles.title2}>Background notifications</Text>
          <View style={styles.rowBetween}>
            <View style={styles.rowBetweenText}>
              <Text style={styles.label}>Notify on agent idle / permission requests</Text>
              <Text style={styles.hint}>
                Keeps a background connection open (a persistent notification while active) so you're
                notified when an agent finishes responding or needs a permission decision, even with
                the app closed.
              </Text>
            </View>
            <Switch value={bgNotifications} onValueChange={toggleBgNotifications} disabled={bgNotifBusy} />
          </View>

          <Pressable style={styles.testButton} onPress={() => Linking.openSettings()}>
            <Text style={styles.testButtonText}>Battery optimization settings</Text>
          </Pressable>
        </>
      )}

      <View style={styles.divider} />

      <Text style={styles.title2}>Super Badger API sources</Text>
      <MetricSourcesSettings />

      <View style={styles.divider} />

      <VpnSettings />
    </ScrollView>
  );
}

// Small fixed swatch colors for the theme picker — deliberately independent
// of `useTheme()` (which only ever reflects the *currently active* theme):
// the picker needs to show all four options' colors at once, including the
// ones not currently selected.
const THEME_PREVIEW: Record<string, {bg: string; primary: string}> = {
  light: {bg: '#ffffff', primary: '#0969da'},
  dark: {bg: '#0d1117', primary: '#2f81f7'},
  slate: {bg: '#1e2530', primary: '#5aa9e6'},
  sepia: {bg: '#f4ecd8', primary: '#a0651b'},
  ember: {bg: '#050403', primary: '#8a3620'},
};

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    scroll: {
      flex: 1,
      backgroundColor: theme.bg,
    },
    container: {
      padding: 20,
      maxWidth: 480,
    },
    title: {
      fontSize: 20,
      fontWeight: '700',
      marginBottom: 20,
      color: theme.text,
    },
    title2: {
      fontSize: 15,
      fontWeight: '700',
      marginBottom: 10,
      color: theme.text,
    },
    themeRow: {
      flexDirection: 'row',
      flexWrap: 'wrap',
      gap: 10,
    },
    themeSwatch: {
      alignItems: 'center',
      gap: 6,
      padding: 10,
      borderRadius: 10,
      borderWidth: 2,
      borderColor: theme.border,
      backgroundColor: theme.surface,
    },
    themeSwatchActive: {
      borderColor: theme.primary,
    },
    themeSwatchColors: {
      flexDirection: 'row',
      borderRadius: 14,
      overflow: 'hidden',
      borderWidth: 1,
      borderColor: theme.border,
    },
    themeSwatchDot: {
      width: 20,
      height: 28,
    },
    themeSwatchLabel: {
      fontSize: 12,
      fontWeight: '600',
      color: theme.text,
    },
    buttonDisabled: {
      opacity: 0.5,
    },
    field: {
      marginBottom: 20,
    },
    label: {
      fontSize: 13,
      fontWeight: '600',
      marginBottom: 6,
      color: theme.textMuted,
    },
    input: {
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      paddingHorizontal: 10,
      paddingVertical: 8,
      fontSize: 15,
      color: theme.text,
      backgroundColor: theme.surface,
    },
    hint: {
      fontSize: 12,
      color: theme.textMuted,
      marginTop: 6,
    },
    testRow: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 10,
      marginTop: 10,
    },
    testButton: {
      alignSelf: 'flex-start',
      paddingHorizontal: 14,
      paddingVertical: 8,
      borderRadius: 6,
      backgroundColor: theme.surfaceAlt,
    },
    testButtonText: {
      fontWeight: '600',
      fontSize: 13,
      color: theme.text,
    },
    testOk: {
      color: theme.success,
      fontSize: 13,
    },
    testError: {
      color: theme.danger,
      fontSize: 13,
      flexShrink: 1,
    },
    button: {
      backgroundColor: theme.primary,
      borderRadius: 8,
      paddingVertical: 12,
      alignItems: 'center',
    },
    buttonText: {
      color: theme.primaryText,
      fontWeight: '600',
    },
    divider: {
      height: 1,
      backgroundColor: theme.border,
      marginVertical: 24,
    },
    rowBetween: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 12,
      marginBottom: 12,
    },
    rowBetweenText: {
      flex: 1,
      minWidth: 0,
      gap: 4,
    },
  });
}
