import React, {useCallback, useEffect, useState} from 'react';
import {
  ActivityIndicator,
  Pressable,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from 'react-native';

import {
  mullvadConfigure,
  mullvadConnect,
  mullvadDisconnect,
  mullvadListRelays,
  mullvadSetLan,
  mullvadSetLocation,
  mullvadStatus,
} from '../api';

// Mirrors the couple of states `mullvad status` output actually starts with;
// anything else (errors, daemon not running, CLI missing) just falls back to
// showing the raw text.
function summarize(output: string): string {
  const first = output.trim().split('\n')[0] ?? '';
  if (/^connected/i.test(first)) {
    return 'Connected';
  }
  if (/^connecting/i.test(first)) {
    return 'Connecting…';
  }
  if (/^disconnected/i.test(first)) {
    return 'Disconnected';
  }
  return first || 'Unknown';
}

export function VpnSettings(): React.JSX.Element {
  const [statusText, setStatusText] = useState('');
  const [statusLoading, setStatusLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const [accountToken, setAccountToken] = useState('');
  const [country, setCountry] = useState('');
  const [city, setCity] = useState('');
  const [lanAllowed, setLanAllowed] = useState(true);
  const [relaysText, setRelaysText] = useState('');
  const [showRelays, setShowRelays] = useState(false);

  const refreshStatus = useCallback(() => {
    setStatusLoading(true);
    mullvadStatus()
      .then(res => setStatusText(res.output))
      .catch(err => setStatusText(`error: ${err.message}`))
      .finally(() => setStatusLoading(false));
  }, []);

  useEffect(() => {
    refreshStatus();
  }, [refreshStatus]);

  const runAction = async (label: string, fn: () => Promise<{output: string}>) => {
    setBusy(true);
    setError('');
    try {
      await fn();
      refreshStatus();
    } catch (err) {
      setError(`${label} failed: ${(err as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  const onConfigure = () => {
    if (!accountToken.trim()) {
      setError('Enter an account token first');
      return;
    }
    runAction('Configure', () => mullvadConfigure(accountToken.trim()));
  };

  const onSetLocation = () => {
    if (!country.trim()) {
      setError('Enter a country code first (e.g. us, se)');
      return;
    }
    runAction('Set location', () =>
      mullvadSetLocation({country: country.trim(), city: city.trim() || undefined}),
    );
  };

  const onToggleLan = (value: boolean) => {
    setLanAllowed(value);
    runAction('LAN setting', () => mullvadSetLan(value));
  };

  const onLoadRelays = async () => {
    setBusy(true);
    setError('');
    try {
      const res = await mullvadListRelays();
      setRelaysText(res.output);
      setShowRelays(true);
    } catch (err) {
      setError(`Load relays failed: ${(err as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <View style={styles.section}>
      <Text style={styles.title}>VPN (Mullvad)</Text>

      <View style={styles.statusRow}>
        {statusLoading ? (
          <ActivityIndicator size="small" />
        ) : (
          <Text style={styles.statusText}>{summarize(statusText)}</Text>
        )}
        <Pressable style={styles.linkButton} onPress={refreshStatus} disabled={busy}>
          <Text style={styles.linkButtonText}>Refresh</Text>
        </Pressable>
      </View>
      {!!statusText && <Text style={styles.statusDetail}>{statusText.trim()}</Text>}

      <View style={styles.buttonRow}>
        <Pressable
          style={[styles.button, styles.buttonHalf]}
          onPress={() => runAction('Connect', mullvadConnect)}
          disabled={busy}>
          <Text style={styles.buttonText}>Connect</Text>
        </Pressable>
        <Pressable
          style={[styles.button, styles.buttonHalf, styles.buttonSecondary]}
          onPress={() => runAction('Disconnect', mullvadDisconnect)}
          disabled={busy}>
          <Text style={[styles.buttonText, styles.buttonSecondaryText]}>Disconnect</Text>
        </Pressable>
      </View>

      <View style={styles.field}>
        <Text style={styles.label}>Account token</Text>
        <TextInput
          style={styles.input}
          value={accountToken}
          onChangeText={setAccountToken}
          placeholder="0000 0000 0000 0000"
          autoCapitalize="none"
          autoCorrect={false}
          secureTextEntry
        />
        <Pressable style={styles.button} onPress={onConfigure} disabled={busy}>
          <Text style={styles.buttonText}>Configure</Text>
        </Pressable>
      </View>

      <View style={styles.field}>
        <Text style={styles.label}>Relay location</Text>
        <View style={styles.buttonRow}>
          <TextInput
            style={[styles.input, styles.inputHalf]}
            value={country}
            onChangeText={setCountry}
            placeholder="Country (e.g. us)"
            autoCapitalize="none"
            autoCorrect={false}
          />
          <TextInput
            style={[styles.input, styles.inputHalf]}
            value={city}
            onChangeText={setCity}
            placeholder="City (optional, e.g. nyc)"
            autoCapitalize="none"
            autoCorrect={false}
          />
        </View>
        <Pressable style={styles.button} onPress={onSetLocation} disabled={busy}>
          <Text style={styles.buttonText}>Set location</Text>
        </Pressable>
        <Pressable style={styles.linkButton} onPress={onLoadRelays} disabled={busy}>
          <Text style={styles.linkButtonText}>
            {showRelays ? 'Reload available locations' : 'Show available locations'}
          </Text>
        </Pressable>
        {showRelays && (
          <Text style={styles.relaysText} selectable>
            {relaysText || '(no output)'}
          </Text>
        )}
      </View>

      <View style={[styles.field, styles.switchRow]}>
        <View style={styles.switchLabel}>
          <Text style={styles.label}>Allow local network</Text>
          <Text style={styles.hint}>
            Keeps traffic to 192.168.*.*, 10.*.*.* and other LAN ranges off the
            tunnel while the VPN is connected.
          </Text>
        </View>
        <Switch value={lanAllowed} onValueChange={onToggleLan} disabled={busy} />
      </View>

      {!!error && <Text style={styles.error}>{error}</Text>}
    </View>
  );
}

const styles = StyleSheet.create({
  section: {
    marginTop: 8,
  },
  title: {
    fontSize: 16,
    fontWeight: '700',
    marginBottom: 12,
  },
  statusRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: 4,
  },
  statusText: {
    fontSize: 15,
    fontWeight: '600',
  },
  statusDetail: {
    fontSize: 12,
    color: '#57606a',
    marginBottom: 16,
  },
  buttonRow: {
    flexDirection: 'row',
    gap: 10,
    marginBottom: 12,
  },
  button: {
    backgroundColor: '#0969da',
    borderRadius: 8,
    paddingVertical: 12,
    alignItems: 'center',
    marginTop: 8,
  },
  buttonHalf: {
    flex: 1,
    marginTop: 0,
  },
  buttonSecondary: {
    backgroundColor: '#eaeef2',
  },
  buttonText: {
    color: '#fff',
    fontWeight: '600',
  },
  buttonSecondaryText: {
    color: '#24292f',
  },
  linkButton: {
    paddingVertical: 6,
  },
  linkButtonText: {
    color: '#0969da',
    fontWeight: '600',
    fontSize: 13,
  },
  field: {
    marginBottom: 20,
  },
  label: {
    fontSize: 13,
    fontWeight: '600',
    marginBottom: 6,
    color: '#57606a',
  },
  input: {
    borderWidth: 1,
    borderColor: '#d0d7de',
    borderRadius: 6,
    paddingHorizontal: 10,
    paddingVertical: 8,
    fontSize: 15,
  },
  inputHalf: {
    flex: 1,
  },
  hint: {
    fontSize: 12,
    color: '#57606a',
    marginTop: 4,
  },
  switchRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  switchLabel: {
    flex: 1,
    marginRight: 12,
  },
  relaysText: {
    marginTop: 10,
    fontSize: 11,
    fontFamily: 'monospace',
    color: '#24292f',
    backgroundColor: '#f6f8fa',
    borderRadius: 6,
    padding: 10,
  },
  error: {
    color: '#cf222e',
    fontSize: 13,
    marginTop: 4,
  },
});
