import React, {useEffect, useMemo, useState} from 'react';
import {ActivityIndicator, GestureResponderEvent, LayoutChangeEvent, Pressable, StyleSheet, Text, View} from 'react-native';
import Svg, {Circle, Line, Path} from 'react-native-svg';

import {getStationDataPointHistory, HistoryBucket, HistoryRange} from '../api';
import {Theme, useTheme} from '../theme';

const RANGES: {label: string; value: HistoryRange}[] = [
  {label: '1h', value: '1h'},
  {label: '3h', value: '3h'},
  {label: '12h', value: '12h'},
  {label: '1d', value: '1d'},
  {label: '1w', value: '1w'},
  {label: '1m', value: '1m'},
];

const CHART_HEIGHT = 160;

// Mobile-first line graph for one numeric data point's history (see
// server/api/history.go): the server already downsamples to ~400 points, so
// this just draws a path — no client-side thinning, no mouse-drag zoom (a
// touch drag is used for the scrub readout instead), no library beyond
// react-native-svg so it renders the same on phone and the Vite web build.
// Range switching is a row of tap targets sized for a thumb, not a dropdown.
export function DataPointChart({stationId, dataKey}: {stationId: number; dataKey: string}): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [range, setRange] = useState<HistoryRange>('1d');
  const [buckets, setBuckets] = useState<HistoryBucket[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [width, setWidth] = useState(0);
  const [scrubIndex, setScrubIndex] = useState<number | null>(null);

  useEffect(() => {
    setLoading(true);
    setError(null);
    getStationDataPointHistory(stationId, dataKey, range)
      .then(setBuckets)
      .catch(err => setError(String(err)))
      .finally(() => setLoading(false));
  }, [stationId, dataKey, range]);

  const {path, points, minValue, maxValue} = useMemo(() => buildPath(buckets, width, CHART_HEIGHT), [buckets, width]);

  const onLayout = (e: LayoutChangeEvent) => setWidth(e.nativeEvent.layout.width);

  const onTouch = (e: GestureResponderEvent) => {
    if (points.length === 0) return;
    const x = e.nativeEvent.locationX;
    let closest = 0;
    let closestDist = Infinity;
    for (let i = 0; i < points.length; i++) {
      const d = Math.abs(points[i].x - x);
      if (d < closestDist) {
        closestDist = d;
        closest = i;
      }
    }
    setScrubIndex(closest);
  };

  const scrubbed = scrubIndex !== null ? buckets[scrubIndex] : null;

  return (
    <View style={styles.container}>
      <View style={styles.rangeRow}>
        {RANGES.map(r => (
          <Pressable
            key={r.value}
            style={[styles.rangeButton, range === r.value && styles.rangeButtonActive]}
            onPress={() => setRange(r.value)}
            hitSlop={6}>
            <Text style={[styles.rangeButtonText, range === r.value && styles.rangeButtonTextActive]}>
              {r.label}
            </Text>
          </Pressable>
        ))}
      </View>

      <View
        style={styles.chartArea}
        onLayout={onLayout}
        onStartShouldSetResponder={() => true}
        onResponderMove={onTouch}
        onResponderGrant={onTouch}
        onResponderRelease={() => setScrubIndex(null)}>
        {loading ? (
          <View style={styles.center}>
            <ActivityIndicator color={theme.text} />
          </View>
        ) : error ? (
          <View style={styles.center}>
            <Text style={styles.error}>{error}</Text>
          </View>
        ) : buckets.length === 0 ? (
          <View style={styles.center}>
            <Text style={styles.hint}>No data yet</Text>
          </View>
        ) : width > 0 ? (
          <Svg width={width} height={CHART_HEIGHT}>
            <Path d={path} stroke={theme.primary} strokeWidth={2} fill="none" />
            {scrubIndex !== null && points[scrubIndex] && (
              <>
                <Line
                  x1={points[scrubIndex].x}
                  y1={0}
                  x2={points[scrubIndex].x}
                  y2={CHART_HEIGHT}
                  stroke={theme.border}
                  strokeWidth={1}
                />
                <Circle cx={points[scrubIndex].x} cy={points[scrubIndex].y} r={4} fill={theme.primary} />
              </>
            )}
          </Svg>
        ) : null}
      </View>

      {!loading && !error && buckets.length > 0 && (
        <View style={styles.footer}>
          <Text style={styles.hint}>
            {scrubbed ? formatTs(scrubbed.ts) : `${buckets.length} points`}
          </Text>
          <Text style={styles.footerValue}>
            {scrubbed ? scrubbed.value.toFixed(2) : `min ${minValue.toFixed(1)} · max ${maxValue.toFixed(1)}`}
          </Text>
        </View>
      )}
    </View>
  );
}

function buildPath(
  buckets: HistoryBucket[],
  width: number,
  height: number,
): {path: string; points: {x: number; y: number}[]; minValue: number; maxValue: number} {
  if (buckets.length === 0 || width === 0) {
    return {path: '', points: [], minValue: 0, maxValue: 0};
  }
  const values = buckets.map(b => b.value);
  const minValue = Math.min(...values);
  const maxValue = Math.max(...values);
  const span = maxValue - minValue || 1;
  const minTs = buckets[0].ts;
  const maxTs = buckets[buckets.length - 1].ts || minTs + 1;
  const tsSpan = maxTs - minTs || 1;

  // Vertical padding so the line never touches the top/bottom edge.
  const pad = height * 0.1;
  const points = buckets.map(b => ({
    x: ((b.ts - minTs) / tsSpan) * width,
    y: height - pad - ((b.value - minValue) / span) * (height - 2 * pad),
  }));

  const path = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ');
  return {path, points, minValue, maxValue};
}

function formatTs(unixSeconds: number): string {
  const d = new Date(unixSeconds * 1000);
  return d.toLocaleString(undefined, {month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'});
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    container: {
      gap: 8,
    },
    rangeRow: {
      flexDirection: 'row',
      flexWrap: 'wrap',
      gap: 6,
    },
    rangeButton: {
      paddingHorizontal: 10,
      paddingVertical: 6,
      borderRadius: 6,
      backgroundColor: theme.surfaceAlt,
      minWidth: 40,
      alignItems: 'center',
    },
    rangeButtonActive: {
      backgroundColor: theme.primary,
    },
    rangeButtonText: {
      fontSize: 12,
      fontWeight: '600',
      color: theme.text,
    },
    rangeButtonTextActive: {
      color: theme.primaryText,
    },
    chartArea: {
      height: CHART_HEIGHT,
      borderRadius: 8,
      backgroundColor: theme.surface,
      borderWidth: 1,
      borderColor: theme.border,
      overflow: 'hidden',
    },
    center: {
      flex: 1,
      alignItems: 'center',
      justifyContent: 'center',
    },
    error: {
      color: theme.danger,
      fontSize: 12,
    },
    hint: {
      fontSize: 12,
      color: theme.textMuted,
    },
    footer: {
      flexDirection: 'row',
      justifyContent: 'space-between',
    },
    footerValue: {
      fontSize: 12,
      fontWeight: '600',
      color: theme.text,
    },
  });
}
