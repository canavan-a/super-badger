import React, {useCallback, useEffect, useState} from 'react';
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
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
import {Theme, useTheme} from '../theme';

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

interface RelayCity {
  code: string;
  name: string;
}

interface RelayCountry {
  code: string;
  name: string;
  cities: RelayCity[];
}

// Parses `mullvad relay list`'s plaintext tree into country/city options for
// the dropdowns below — three indent levels (country, city, individual
// relay hostnames), only the first two of which this needs:
//   Albania (al)
//       Tirana (tia) @ 41.32795°N, 19.81902°W
//           al-tia-wg-001 (...) - hosted by ...
// A country/city line is "Name (code)", optionally followed by " @ ..."
// for cities — matched by indentation (country: none, city: exactly one
// level) rather than by content, since names themselves can contain
// parentheses-free text freely.
function parseRelayList(output: string): RelayCountry[] {
  const countries: RelayCountry[] = [];
  let current: RelayCountry | null = null;
  for (const rawLine of output.split('\n')) {
    if (!rawLine.trim()) continue;
    const indent = rawLine.length - rawLine.trimStart().length;
    const line = rawLine.trim();
    const match = line.match(/^(.+?)\s+\(([a-z0-9-]+)\)(\s+@.*)?$/i);
    if (!match) continue;
    const [, name, code, atSuffix] = match;
    if (indent === 0) {
      current = {code, name, cities: []};
      countries.push(current);
    } else if (indent > 0 && atSuffix && current) {
      // Only a city line carries " @ lat,lon"; deeper relay-hostname lines
      // don't match the "Name (code)" shape at all (hostnames have no
      // spaces before their parenthesized IPs) and so are skipped above.
      current.cities.push({code, name});
    }
  }
  return countries;
}

export function VpnSettings(): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [statusText, setStatusText] = useState('');
  const [statusLoading, setStatusLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const [accountToken, setAccountToken] = useState('');
  const [country, setCountry] = useState('');
  const [city, setCity] = useState('');
  const [lanAllowed, setLanAllowed] = useState(true);
  const [countries, setCountries] = useState<RelayCountry[]>([]);
  const [relaysLoading, setRelaysLoading] = useState(true);
  const [countryOpen, setCountryOpen] = useState(false);
  const [cityOpen, setCityOpen] = useState(false);

  const selectedCountry = countries.find(c => c.code === country);
  const selectedCity = selectedCountry?.cities.find(c => c.code === city);

  const refreshStatus = useCallback(() => {
    setStatusLoading(true);
    mullvadStatus()
      .then(res => setStatusText(res.output))
      .catch(err => setStatusText(`error: ${err.message}`))
      .finally(() => setStatusLoading(false));
  }, []);

  const refreshRelays = useCallback(() => {
    setRelaysLoading(true);
    mullvadListRelays()
      .then(res => setCountries(parseRelayList(res.output)))
      .catch(err => setError(`Load relays failed: ${err.message}`))
      .finally(() => setRelaysLoading(false));
  }, []);

  useEffect(() => {
    refreshStatus();
  }, [refreshStatus]);

  useEffect(() => {
    refreshRelays();
  }, [refreshRelays]);

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
    if (!country) {
      setError('Pick a country first');
      return;
    }
    runAction('Set location', () => mullvadSetLocation({country, city: city || undefined}));
  };

  const onToggleLan = (value: boolean) => {
    setLanAllowed(value);
    runAction('LAN setting', () => mullvadSetLan(value));
  };

  const selectCountry = (code: string) => {
    setCountry(code);
    setCity('');
    setCountryOpen(false);
  };

  const selectCity = (code: string) => {
    setCity(code);
    setCityOpen(false);
  };

  return (
    <View style={styles.section}>
      <Text style={styles.title}>VPN (Mullvad)</Text>

      <View style={styles.statusRow}>
        {statusLoading ? (
          <ActivityIndicator size="small" color={theme.text} />
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
          <View style={styles.dropdownHalf}>
            <Pressable
              style={styles.dropdownButton}
              onPress={() => {
                setCountryOpen(o => !o);
                setCityOpen(false);
              }}
              disabled={relaysLoading || countries.length === 0}>
              <Text style={styles.dropdownButtonText} numberOfLines={1}>
                {relaysLoading
                  ? 'Loading…'
                  : selectedCountry
                  ? `${selectedCountry.name} (${selectedCountry.code})`
                  : 'Country'}
              </Text>
              <Text style={styles.dropdownCaret}>{countryOpen ? '▴' : '▾'}</Text>
            </Pressable>
            {countryOpen && (
              <ScrollView style={styles.dropdownList} nestedScrollEnabled>
                {countries.map(c => (
                  <Pressable key={c.code} style={styles.dropdownOption} onPress={() => selectCountry(c.code)}>
                    <Text style={styles.dropdownOptionText}>
                      {c.name} ({c.code})
                    </Text>
                  </Pressable>
                ))}
              </ScrollView>
            )}
          </View>

          <View style={styles.dropdownHalf}>
            <Pressable
              style={styles.dropdownButton}
              onPress={() => {
                setCityOpen(o => !o);
                setCountryOpen(false);
              }}
              disabled={!selectedCountry || selectedCountry.cities.length === 0}>
              <Text style={styles.dropdownButtonText} numberOfLines={1}>
                {selectedCity ? `${selectedCity.name} (${selectedCity.code})` : 'Any city'}
              </Text>
              <Text style={styles.dropdownCaret}>{cityOpen ? '▴' : '▾'}</Text>
            </Pressable>
            {cityOpen && selectedCountry && (
              <ScrollView style={styles.dropdownList} nestedScrollEnabled>
                <Pressable style={styles.dropdownOption} onPress={() => selectCity('')}>
                  <Text style={styles.dropdownOptionText}>Any city</Text>
                </Pressable>
                {selectedCountry.cities.map(c => (
                  <Pressable key={c.code} style={styles.dropdownOption} onPress={() => selectCity(c.code)}>
                    <Text style={styles.dropdownOptionText}>
                      {c.name} ({c.code})
                    </Text>
                  </Pressable>
                ))}
              </ScrollView>
            )}
          </View>
        </View>
        <Pressable style={styles.button} onPress={onSetLocation} disabled={busy}>
          <Text style={styles.buttonText}>Set location</Text>
        </Pressable>
        <Pressable style={styles.linkButton} onPress={refreshRelays} disabled={relaysLoading}>
          <Text style={styles.linkButtonText}>{relaysLoading ? 'Loading…' : 'Reload available locations'}</Text>
        </Pressable>
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

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    section: {
      marginTop: 8,
    },
    title: {
      fontSize: 16,
      fontWeight: '700',
      marginBottom: 12,
      color: theme.text,
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
      color: theme.text,
    },
    statusDetail: {
      fontSize: 12,
      color: theme.textMuted,
      marginBottom: 16,
    },
    buttonRow: {
      flexDirection: 'row',
      gap: 10,
      marginBottom: 12,
    },
    button: {
      backgroundColor: theme.primary,
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
      backgroundColor: theme.surfaceAlt,
    },
    buttonText: {
      color: theme.primaryText,
      fontWeight: '600',
    },
    buttonSecondaryText: {
      color: theme.text,
    },
    linkButton: {
      paddingVertical: 6,
    },
    linkButtonText: {
      color: theme.primary,
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
    dropdownHalf: {
      flex: 1,
    },
    dropdownButton: {
      flexDirection: 'row',
      alignItems: 'center',
      justifyContent: 'space-between',
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      paddingHorizontal: 10,
      paddingVertical: 8,
      backgroundColor: theme.surface,
    },
    dropdownButtonText: {
      flex: 1,
      fontSize: 14,
      color: theme.text,
    },
    dropdownCaret: {
      fontSize: 12,
      color: theme.textMuted,
      marginLeft: 6,
    },
    dropdownList: {
      maxHeight: 220,
      marginTop: 4,
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      backgroundColor: theme.surface,
    },
    dropdownOption: {
      paddingHorizontal: 10,
      paddingVertical: 8,
      borderBottomWidth: 1,
      borderBottomColor: theme.border,
    },
    dropdownOptionText: {
      fontSize: 13,
      color: theme.text,
    },
    hint: {
      fontSize: 12,
      color: theme.textMuted,
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
    error: {
      color: theme.danger,
      fontSize: 13,
      marginTop: 4,
    },
  });
}
