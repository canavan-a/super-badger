import React, {useCallback, useEffect, useState} from 'react';
import {ActivityIndicator, Pressable, ScrollView, StyleSheet, Switch, Text, TextInput, View} from 'react-native';

import type {HeaderInfo} from '../App';
import {getStation, getStationDataPoints, Station, StationDataPoint, updateStationDataPointSettings} from '../api';
import {DataPointChart} from '../components/DataPointChart';
import {Theme, useTheme} from '../theme';

// Per-station settings tab for the Super Badger Station Standard API: shows
// every ad hoc data point the server has polled for this station (see
// server/metrics), how recently it updated, a toggle to surface it on the
// station's top bar, and an optional above/below threshold that triggers a
// push notification when crossed (server/api/ws.go's datapoint_threshold).
export function StationSettingsScreen({
  stationId,
  onHeaderChange,
}: {
  stationId: number;
  onHeaderChange: (info: HeaderInfo | null) => void;
}): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [station, setStation] = useState<Station | null>(null);
  const [points, setPoints] = useState<StationDataPoint[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [expandedKey, setExpandedKey] = useState<string | null>(null);

  const refresh = useCallback(() => {
    Promise.all([getStation(stationId), getStationDataPoints(stationId)])
      .then(([st, pts]) => {
        setStation(st);
        setPoints(pts);
      })
      .catch(err => setError(String(err)))
      .finally(() => setLoading(false));
  }, [stationId]);

  useEffect(() => {
    refresh();
    const interval = setInterval(refresh, 10000);
    return () => clearInterval(interval);
  }, [refresh]);

  useEffect(() => {
    onHeaderChange({title: station ? `${station.name} · Data Points` : 'Data Points'});
    return () => onHeaderChange(null);
  }, [station, onHeaderChange]);

  const patch = (key: string, updates: Partial<StationDataPoint>) => {
    setPoints(prev => prev.map(p => (p.key === key ? {...p, ...updates} : p)));
  };

  const save = async (point: StationDataPoint) => {
    try {
      await updateStationDataPointSettings(stationId, point.key, {
        show_on_top_bar: point.show_on_top_bar,
        threshold_enabled: point.threshold_enabled,
        threshold_value: point.threshold_value,
        threshold_direction: point.threshold_direction,
      });
    } catch (err) {
      setError(String(err));
    }
  };

  if (loading) {
    return (
      <View style={styles.center}>
        <ActivityIndicator color={theme.text} />
      </View>
    );
  }

  return (
    <ScrollView style={styles.scroll} contentContainerStyle={styles.container}>
      {error && <Text style={styles.error}>{error}</Text>}
      {points.length === 0 ? (
        <Text style={styles.hint}>
          No data points reported for this station yet — configure a metric source in Settings that
          covers it.
        </Text>
      ) : (
        points.map(point => (
          <View key={point.key} style={styles.card}>
            <Pressable
              style={styles.rowBetween}
              onPress={() => setExpandedKey(k => (k === point.key ? null : point.key))}>
              <View style={styles.rowBetweenText}>
                <Text style={styles.label}>{point.key}</Text>
                <Text style={styles.hint}>
                  {point.value} · updated {formatRecency(point.updated_at)}
                </Text>
              </View>
              <Text style={styles.expandIcon}>{expandedKey === point.key ? '▾' : '▸'}</Text>
            </Pressable>

            {expandedKey === point.key && <DataPointChart stationId={stationId} dataKey={point.key} />}

            <View style={styles.rowBetween}>
              <Text style={styles.subLabel}>Show on top bar</Text>
              <Switch
                value={point.show_on_top_bar}
                onValueChange={v => {
                  const next = {...point, show_on_top_bar: v};
                  patch(point.key, {show_on_top_bar: v});
                  save(next);
                }}
              />
            </View>

            <View style={styles.rowBetween}>
              <Text style={styles.subLabel}>Notify on threshold</Text>
              <Switch
                value={point.threshold_enabled}
                onValueChange={v => {
                  const next = {...point, threshold_enabled: v};
                  patch(point.key, {threshold_enabled: v});
                  save(next);
                }}
              />
            </View>

            {point.threshold_enabled && (
              <View style={styles.thresholdRow}>
                <Pressable
                  onPress={() => {
                    const dir = point.threshold_direction === 'above' ? 'below' : 'above';
                    const next = {...point, threshold_direction: dir as 'above' | 'below'};
                    patch(point.key, {threshold_direction: dir as 'above' | 'below'});
                    save(next);
                  }}
                  style={styles.directionButton}>
                  <Text style={styles.directionButtonText}>{point.threshold_direction}</Text>
                </Pressable>
                <TextInput
                  style={styles.thresholdInput}
                  keyboardType="numeric"
                  value={String(point.threshold_value)}
                  onChangeText={text => {
                    const v = parseFloat(text);
                    patch(point.key, {threshold_value: Number.isFinite(v) ? v : 0});
                  }}
                  onBlur={() => {
                    const p = points.find(pp => pp.key === point.key);
                    if (p) save(p);
                  }}
                />
              </View>
            )}
          </View>
        ))
      )}
    </ScrollView>
  );
}

function formatRecency(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 0 || Number.isNaN(ms)) return 'just now';
  const secs = Math.round(ms / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  return `${hours}h ago`;
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    scroll: {
      flex: 1,
      backgroundColor: theme.bg,
    },
    container: {
      padding: 16,
      gap: 12,
    },
    center: {
      flex: 1,
      alignItems: 'center',
      justifyContent: 'center',
    },
    error: {
      color: theme.danger,
      marginBottom: 12,
    },
    hint: {
      fontSize: 12,
      color: theme.textMuted,
    },
    card: {
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 8,
      padding: 12,
      backgroundColor: theme.surface,
      gap: 8,
    },
    label: {
      fontSize: 14,
      fontWeight: '700',
      color: theme.text,
    },
    subLabel: {
      fontSize: 13,
      color: theme.text,
    },
    expandIcon: {
      fontSize: 14,
      color: theme.textMuted,
    },
    rowBetween: {
      flexDirection: 'row',
      alignItems: 'center',
      justifyContent: 'space-between',
      gap: 12,
    },
    rowBetweenText: {
      flex: 1,
      minWidth: 0,
      gap: 2,
    },
    thresholdRow: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 8,
    },
    directionButton: {
      paddingHorizontal: 10,
      paddingVertical: 6,
      borderRadius: 6,
      backgroundColor: theme.surfaceAlt,
    },
    directionButtonText: {
      fontSize: 12,
      fontWeight: '600',
      color: theme.text,
    },
    thresholdInput: {
      flex: 1,
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      paddingHorizontal: 10,
      paddingVertical: 6,
      fontSize: 14,
      color: theme.text,
      backgroundColor: theme.surface,
    },
  });
}
