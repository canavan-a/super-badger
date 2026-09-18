import React, {useEffect, useState} from 'react';
import {Pressable, StyleSheet, Text, View} from 'react-native';

import {
  formatDataPointValue,
  formatTokens,
  getStationDataPoints,
  getStationUsage,
  Station,
  StationDataPoint,
  TokenUsage,
} from '../api';
import {Theme, useTheme} from '../theme';

// The station list's own card, with the same live top-bar badges/token count
// the station's chat header shows — so "is anything worth checking on?" is
// answerable from the list without opening each station. Polls its own data
// independently (rather than the list screen fetching for every card at
// once) so one station's data points/usage endpoint being slow doesn't hold
// up the rest of the list.
export function StationCard({station, onPress}: {station: Station; onPress: () => void}): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [points, setPoints] = useState<StationDataPoint[]>([]);
  const [usage, setUsage] = useState<TokenUsage | null>(null);

  useEffect(() => {
    const refresh = () => {
      getStationDataPoints(station.id)
        .then(setPoints)
        .catch(() => {});
      if (station.top_bar_actions.includes('tokens')) {
        getStationUsage(station.id)
          .then(setUsage)
          .catch(() => {});
      }
    };
    refresh();
    const interval = setInterval(refresh, 10000);
    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [station.id, station.top_bar_actions.join(',')]);

  const badges = points
    .filter(p => p.show_on_top_bar)
    .map(p => ({label: p.label || p.key, value: formatDataPointValue(p.value, p.decimals)}));

  const activeContext = usage ? usage.input + usage.output + usage.cache_read + usage.cache_write : 0;
  const showTokenCount = station.top_bar_actions.includes('tokens') && activeContext > 0;

  return (
    <Pressable style={styles.card} onPress={onPress}>
      {
        // A real View, not borderLeftColor on this rounded card — Android's
        // border renderer doesn't reliably draw a differently-colored side
        // once borderRadius is set (it was silently falling back to the
        // plain theme.border color on every station regardless of
        // station.color, confirmed via logcat: the value was always
        // correct, it just never reached the screen). overflow:hidden on
        // .card clips this to the card's rounded left corners.
      }
      <View style={[styles.accentBar, {backgroundColor: station.color}]} />
      <View style={styles.cardHeader}>
        <View style={styles.titleRow}>
          <Text style={styles.cardTitle}>{station.name}</Text>
          {showTokenCount ? <Text style={styles.tokenCount}>{formatTokens(activeContext)}</Text> : null}
        </View>
        <View style={styles.statusRow}>
          <View style={[styles.dot, {backgroundColor: station.reachable ? theme.success : theme.danger}]} />
          <Text style={styles.cardStatus}>{station.reachable ? station.status : 'unreachable'}</Text>
        </View>
      </View>
      {badges.length > 0 && (
        <View style={styles.badgeRow}>
          {badges.map(b => (
            <View key={b.label} style={styles.badge}>
              <Text style={styles.badgeLabel}>{b.label}</Text>
              <Text style={styles.badgeValue}>{b.value}</Text>
            </View>
          ))}
        </View>
      )}
    </Pressable>
  );
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    card: {
      borderWidth: 1,
      borderColor: theme.border,
      borderRadius: 10,
      padding: 14,
      paddingLeft: 17,
      backgroundColor: theme.surface,
      gap: 8,
      overflow: 'hidden',
      position: 'relative',
    },
    accentBar: {
      position: 'absolute',
      left: 0,
      top: 0,
      bottom: 0,
      width: 4,
    },
    cardHeader: {
      flexDirection: 'row',
      alignItems: 'center',
      justifyContent: 'space-between',
    },
    titleRow: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 8,
      flexShrink: 1,
      minWidth: 0,
    },
    cardTitle: {
      fontSize: 16,
      fontWeight: '700',
      color: theme.text,
    },
    tokenCount: {
      fontSize: 15,
      fontWeight: '700',
      color: theme.textMuted,
    },
    statusRow: {
      flexDirection: 'row',
      alignItems: 'center',
      gap: 6,
    },
    dot: {
      width: 7,
      height: 7,
      borderRadius: 4,
    },
    cardStatus: {
      fontSize: 11,
      fontWeight: '600',
      textTransform: 'uppercase',
      color: theme.textMuted,
    },
    badgeRow: {
      flexDirection: 'row',
      flexWrap: 'wrap',
      gap: 4,
    },
    badge: {
      flexDirection: 'row',
      alignItems: 'baseline',
      gap: 3,
      paddingHorizontal: 6,
      paddingVertical: 2,
      borderRadius: 8,
      backgroundColor: theme.surfaceAlt,
    },
    badgeLabel: {
      fontSize: 10,
      fontWeight: '600',
      color: theme.textMuted,
    },
    badgeValue: {
      fontSize: 10,
      fontWeight: '700',
      color: theme.text,
    },
  });
}
