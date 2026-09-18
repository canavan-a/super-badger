import React, {useCallback, useEffect, useRef, useState} from 'react';
import {
  ActivityIndicator,
  Animated,
  PanResponder,
  Pressable,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from 'react-native';

import type {HeaderInfo} from '../App';
import {
  formatDataPointValue,
  getStation,
  getStationDataPoints,
  hideStationDataPoint,
  reorderStationDataPoints,
  Station,
  StationDataPoint,
  TopBarActionKey,
  updateStation,
  updateStationDataPointSettings,
} from '../api';
import {DataPointChart} from '../components/DataPointChart';
import {Icon} from '../components/Icon';
import {platformConfirm} from '../platformConfirm';
import {Route} from '../routes';
import {STATION_COLORS} from '../stationColors';
import {Theme, useTheme} from '../theme';

// Per-station settings tab for the Super Badger Station Standard API: shows
// every ad hoc data point the server has polled for this station (see
// server/metrics), how recently it updated, a toggle to surface it on the
// station's top bar, and an optional above/below threshold that triggers a
// push notification when crossed (server/api/ws.go's datapoint_threshold).
export function StationSettingsScreen({
  stationId,
  onHeaderChange,
  onNavigate,
}: {
  stationId: number;
  onHeaderChange: (info: HeaderInfo | null) => void;
  onNavigate: (route: Route) => void;
}): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [station, setStation] = useState<Station | null>(null);
  const [points, setPoints] = useState<StationDataPoint[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [expandedKey, setExpandedKey] = useState<string | null>(null);
  const [aliasDraft, setAliasDraft] = useState('');

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
    onHeaderChange({
      title: station ? `${station.name} · Data Points` : 'Data Points',
      // Replaces the "⚙" used to get here from the chat screen with a way
      // back to it, rather than leaving the owner stuck on this tab.
      actions: [
        {label: 'Chat', icon: 'chevron-left', onPress: () => onNavigate({name: 'stationDetail', id: stationId})},
      ],
    });
    return () => onHeaderChange(null);
  }, [station, stationId, onHeaderChange, onNavigate]);

  useEffect(() => {
    setAliasDraft(station?.alias ?? '');
  }, [station?.alias]);

  const saveAlias = async () => {
    const trimmed = aliasDraft.trim();
    try {
      const updated = await updateStation(stationId, {alias: trimmed || null});
      setStation(updated);
    } catch (err) {
      setError(String(err));
    }
  };

  const saveColor = async (color: string) => {
    try {
      const updated = await updateStation(stationId, {color});
      setStation(updated);
    } catch (err) {
      setError(String(err));
    }
  };

  const toggleTopBarAction = async (key: TopBarActionKey, enabled: boolean) => {
    if (!station) return;
    const next = enabled
      ? [...station.top_bar_actions, key]
      : station.top_bar_actions.filter(a => a !== key);
    setStation({...station, top_bar_actions: next});
    try {
      const updated = await updateStation(stationId, {top_bar_actions: next});
      setStation(updated);
    } catch (err) {
      setError(String(err));
    }
  };

  const hide = (key: string) => {
    const point = points.find(p => p.key === key);
    platformConfirm(
      'Hide data point',
      `Hide "${point?.label || key}" until fresh data arrives for it?`,
      'Hide',
      async () => {
        setPoints(prev => prev.filter(p => p.key !== key));
        try {
          await hideStationDataPoint(stationId, key);
        } catch (err) {
          setError(String(err));
        }
      },
    );
  };

  const reorder = async (from: number, to: number) => {
    if (from === to) return;
    setPoints(prev => {
      const next = prev.slice();
      const [moved] = next.splice(from, 1);
      next.splice(to, 0, moved);
      reorderStationDataPoints(stationId, next.map(p => p.key)).catch(err => setError(String(err)));
      return next;
    });
  };

  // Merges `updates` into the point and saves it in one step (rather than a
  // separate patch-then-save) so a rapid patch immediately followed by a
  // save (e.g. tapping the decimals stepper) always persists the value just
  // set, not whatever was in `points` before React re-renders.
  const patch = (key: string, updates: Partial<StationDataPoint>) => {
    setPoints(prev => prev.map(p => (p.key === key ? {...p, ...updates} : p)));
  };

  const patchAndSave = (key: string, updates: Partial<StationDataPoint>) => {
    setPoints(prev =>
      prev.map(p => {
        if (p.key !== key) return p;
        const next = {...p, ...updates};
        save(next);
        return next;
      }),
    );
  };

  const save = async (point: StationDataPoint) => {
    try {
      await updateStationDataPointSettings(stationId, point.key, {
        label: point.label,
        decimals: point.decimals,
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

  const topBarPoints = points.filter(p => p.show_on_top_bar);

  return (
    <ScrollView style={styles.scroll} contentContainerStyle={styles.container}>
      {error && <Text style={styles.error}>{error}</Text>}

      <View style={styles.card}>
        <Text style={styles.label}>Station identity</Text>

        <View style={styles.rowBetween}>
          <Text style={styles.subLabel}>Alias / tag</Text>
          <View style={styles.aliasInputRow}>
            <TextInput
              style={styles.labelInput}
              value={aliasDraft}
              onChangeText={setAliasDraft}
              onBlur={saveAlias}
              placeholder="none"
              placeholderTextColor={theme.textMuted}
              autoCapitalize="none"
            />
            <Pressable style={styles.saveButton} onPress={saveAlias}>
              <Text style={styles.saveButtonText}>Save</Text>
            </Pressable>
          </View>
        </View>
        <Text style={styles.hint}>
          Metric sources can target this station by this tag instead of its ID or exact name.
        </Text>

        <View style={styles.rowBetween}>
          <Text style={styles.subLabel}>Color</Text>
          <View style={styles.colorRow}>
            {STATION_COLORS.map(color => (
              <Pressable
                key={color}
                style={[styles.colorSwatch, {backgroundColor: color}]}
                onPress={() => saveColor(color)}>
                {station?.color === color ? <Icon name="check" size={13} color="#fff" strokeWidth={3} /> : null}
              </Pressable>
            ))}
          </View>
        </View>
      </View>

      <View style={styles.card}>
        <Text style={styles.label}>Top bar</Text>
        {topBarPoints.length === 0 ? (
          <Text style={styles.hint}>Nothing shown on the top bar yet.</Text>
        ) : (
          <View style={styles.topBarList}>
            {topBarPoints.map(p => (
              <Pressable
                key={p.key}
                style={styles.topBarRow}
                onPress={() => patchAndSave(p.key, {show_on_top_bar: false})}>
                <Text style={styles.topBarRowLabel}>{p.label || p.key}</Text>
                <Text style={styles.topBarRowValue}>{formatDataPointValue(p.value, p.decimals)}</Text>
                <Icon name="close" size={13} color={theme.textMuted} strokeWidth={2.5} />
              </Pressable>
            ))}
          </View>
        )}
        <Text style={styles.hint}>Tap a button above to remove it from the top bar.</Text>

        <View style={styles.divider} />

        <Text style={styles.subLabel}>Badges</Text>
        {TOP_BAR_BADGE_OPTIONS.map(opt => (
          <View key={opt.key} style={styles.rowBetween}>
            <Text style={styles.subLabel}>{opt.label}</Text>
            <Switch
              value={station?.top_bar_actions.includes(opt.key) ?? false}
              onValueChange={v => toggleTopBarAction(opt.key, v)}
            />
          </View>
        ))}

        <View style={styles.divider} />

        <Text style={styles.subLabel}>Actions</Text>
        {TOP_BAR_ACTION_OPTIONS.map(opt => (
          <View key={opt.key} style={styles.rowBetween}>
            <Text style={styles.subLabel}>{opt.label}</Text>
            <Switch
              value={station?.top_bar_actions.includes(opt.key) ?? false}
              onValueChange={v => toggleTopBarAction(opt.key, v)}
            />
          </View>
        ))}
      </View>

      {points.length === 0 ? (
        <Text style={styles.hint}>
          No data points reported for this station yet — configure a metric source in Settings that
          covers it.
        </Text>
      ) : (
        points.map((point, index) => (
          <PointCard
            key={point.key}
            point={point}
            index={index}
            count={points.length}
            expanded={expandedKey === point.key}
            onToggleExpand={() => setExpandedKey(k => (k === point.key ? null : point.key))}
            onHide={() => hide(point.key)}
            onMove={to => reorder(index, to)}
            onPatch={updates => patch(point.key, updates)}
            onPatchAndSave={updates => patchAndSave(point.key, updates)}
            onSave={() => {
              const p = points.find(pp => pp.key === point.key);
              if (p) save(p);
            }}
            stationId={stationId}
            styles={styles}
            theme={theme}
          />
        ))
      )}
    </ScrollView>
  );
}

const ROW_HEIGHT_ESTIMATE = 90;

function PointCard({
  point,
  index,
  count,
  expanded,
  onToggleExpand,
  onHide,
  onMove,
  onPatch,
  onPatchAndSave,
  onSave,
  stationId,
  styles,
  theme,
}: {
  point: StationDataPoint;
  index: number;
  count: number;
  expanded: boolean;
  onToggleExpand: () => void;
  onHide: () => void;
  onMove: (to: number) => void;
  onPatch: (updates: Partial<StationDataPoint>) => void;
  onPatchAndSave: (updates: Partial<StationDataPoint>) => void;
  onSave: () => void;
  stationId: number;
  styles: ReturnType<typeof makeStyles>;
  theme: Theme;
}): React.JSX.Element {
  const translateY = useRef(new Animated.Value(0)).current;
  const dragging = useRef(false);

  const panResponder = useRef(
    PanResponder.create({
      onStartShouldSetPanResponder: () => true,
      onMoveShouldSetPanResponder: (_evt, gesture) => Math.abs(gesture.dy) > 4,
      onPanResponderGrant: () => {
        dragging.current = true;
      },
      onPanResponderMove: Animated.event([null, {dy: translateY}], {useNativeDriver: false}),
      onPanResponderRelease: (_evt, gesture) => {
        dragging.current = false;
        const offset = Math.round(gesture.dy / ROW_HEIGHT_ESTIMATE);
        const target = Math.max(0, Math.min(count - 1, index + offset));
        Animated.timing(translateY, {toValue: 0, duration: 120, useNativeDriver: false}).start();
        onMove(target);
      },
    }),
  ).current;

  return (
    <Animated.View style={[styles.card, {transform: [{translateY}], zIndex: dragging.current ? 1 : 0}]}>
      <View style={styles.rowBetween}>
        <View {...panResponder.panHandlers} style={styles.dragHandle}>
          <Icon name="drag-handle" size={16} color={theme.textMuted} />
        </View>
        <Pressable style={styles.rowBetweenText} onPress={onToggleExpand}>
          <Text style={styles.label}>{point.label || point.key}</Text>
          {point.label ? <Text style={styles.hint}>{point.key}</Text> : null}
          <Text style={styles.hint}>
            {formatDataPointValue(point.value, point.decimals)} · updated {formatRecency(point.updated_at)}
          </Text>
        </Pressable>
        <Pressable style={styles.trashButton} onPress={onHide} accessibilityLabel="Hide until new data">
          <Icon name="trash" size={17} color={theme.textMuted} />
        </Pressable>
        <Pressable style={styles.expandButton} onPress={onToggleExpand}>
          <Icon name={expanded ? 'chevron-down' : 'chevron-right'} size={16} color={theme.text} />
        </Pressable>
      </View>

      {expanded && <DataPointChart stationId={stationId} dataKey={point.key} decimals={point.decimals} />}

      <View style={styles.rowBetween}>
        <Text style={styles.subLabel}>Label</Text>
        <TextInput
          style={styles.labelInput}
          value={point.label}
          onChangeText={text => onPatch({label: text})}
          onBlur={onSave}
          placeholder={point.key}
          placeholderTextColor={theme.textMuted}
        />
      </View>

      <View style={styles.rowBetween}>
        <Text style={styles.subLabel}>Decimals</Text>
        <View style={styles.decimalsStepper}>
          <Pressable
            style={[styles.decimalsButton, point.decimals <= 0 && styles.decimalsButtonDisabled]}
            onPress={() => onPatchAndSave({decimals: Math.max(0, point.decimals - 1)})}
            disabled={point.decimals <= 0}>
            <Text style={styles.decimalsButtonText}>−</Text>
          </Pressable>
          <Text style={styles.decimalsValue}>{point.decimals}</Text>
          <Pressable
            style={[styles.decimalsButton, point.decimals >= 6 && styles.decimalsButtonDisabled]}
            onPress={() => onPatchAndSave({decimals: Math.min(6, point.decimals + 1)})}
            disabled={point.decimals >= 6}>
            <Text style={styles.decimalsButtonText}>+</Text>
          </Pressable>
        </View>
      </View>

      <View style={styles.rowBetween}>
        <Text style={styles.subLabel}>Show on top bar</Text>
        <Switch value={point.show_on_top_bar} onValueChange={v => onPatchAndSave({show_on_top_bar: v})} />
      </View>

      <View style={styles.rowBetween}>
        <Text style={styles.subLabel}>Notify on threshold</Text>
        <Switch value={point.threshold_enabled} onValueChange={v => onPatchAndSave({threshold_enabled: v})} />
      </View>

      {point.threshold_enabled && (
        <View style={styles.thresholdRow}>
          <Pressable
            onPress={() =>
              onPatchAndSave({threshold_direction: point.threshold_direction === 'above' ? 'below' : 'above'})
            }
            style={styles.directionButton}>
            <Text style={styles.directionButtonText}>{point.threshold_direction}</Text>
          </Pressable>
          <TextInput
            style={styles.thresholdInput}
            keyboardType="numeric"
            value={String(point.threshold_value)}
            onChangeText={text => {
              const v = parseFloat(text);
              onPatch({threshold_value: Number.isFinite(v) ? v : 0});
            }}
            onBlur={onSave}
          />
        </View>
      )}
    </Animated.View>
  );
}

const TOP_BAR_BADGE_OPTIONS: {key: TopBarActionKey; label: string}[] = [
  {key: 'tokens', label: 'Token count'},
];

const TOP_BAR_ACTION_OPTIONS: {key: TopBarActionKey; label: string}[] = [
  {key: 'compact', label: 'Compact'},
  {key: 'reset', label: 'Reset'},
  {key: 'delete', label: 'Delete'},
];

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
    expandButton: {
      padding: 6,
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
    labelInput: {
      flex: 1,
      maxWidth: 180,
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 6,
      paddingHorizontal: 10,
      paddingVertical: 6,
      fontSize: 13,
      color: theme.text,
      backgroundColor: theme.surface,
      textAlign: 'right',
    },
    aliasInputRow: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 6,
    },
    saveButton: {
      paddingHorizontal: 10,
      paddingVertical: 6,
      borderRadius: 6,
      backgroundColor: theme.primary,
    },
    saveButtonText: {
      fontSize: 12,
      fontWeight: '600',
      color: theme.primaryText,
    },
    decimalsStepper: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 10,
    },
    decimalsButton: {
      width: 28,
      height: 28,
      borderRadius: 6,
      backgroundColor: theme.surfaceAlt,
      alignItems: 'center',
      justifyContent: 'center',
    },
    decimalsButtonDisabled: {
      opacity: 0.4,
    },
    decimalsButtonText: {
      fontSize: 16,
      fontWeight: '700',
      color: theme.text,
    },
    decimalsValue: {
      fontSize: 13,
      fontWeight: '600',
      color: theme.text,
      minWidth: 14,
      textAlign: 'center',
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
    divider: {
      height: 1,
      backgroundColor: theme.border,
    },
    colorRow: {
      flexDirection: 'row',
      gap: 8,
    },
    colorSwatch: {
      width: 26,
      height: 26,
      borderRadius: 13,
      alignItems: 'center',
      justifyContent: 'center',
    },
    trashButton: {
      padding: 6,
    },
    dragHandle: {
      padding: 4,
    },
    topBarList: {
      gap: 6,
    },
    topBarRow: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 8,
      paddingHorizontal: 10,
      paddingVertical: 8,
      borderRadius: 6,
      backgroundColor: theme.surfaceAlt,
    },
    topBarRowLabel: {
      flex: 1,
      minWidth: 0,
      fontSize: 13,
      fontWeight: '600',
      color: theme.text,
    },
    topBarRowValue: {
      fontSize: 13,
      fontWeight: '700',
      color: theme.text,
    },
  });
}
