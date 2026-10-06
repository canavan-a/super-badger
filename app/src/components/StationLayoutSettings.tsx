import React, {useCallback, useEffect, useRef, useState} from 'react';
import {
  ActivityIndicator,
  PanResponder,
  StyleSheet,
  Text,
  View,
} from 'react-native';

import {listStations, Station, updateStationLayout} from '../api';
import {Theme, useTheme} from '../theme';
import {Icon} from './Icon';

// Settings → Stations: the Active list (home list, drawer and swipe rotation,
// in this order) and the Hidden list (still configured — session, data
// points, notifications — just not shown or rotated through). Both live in
// one list split by a "Hidden" divider row, so a single drag both reorders
// and moves a station between the two: drop it below the divider to hide it.
//
// Built on core PanResponder (like App.tsx's edge swipe) rather than a
// drag-and-drop library, which would need native linking. Every row is the
// same fixed height, divider included, so a drag's distance maps straight to
// a target slot — no per-row measuring.

const ROW_HEIGHT = 56;

type Item = {kind: 'station'; station: Station} | {kind: 'divider'};

const DIVIDER: Item = {kind: 'divider'};

const itemKey = (item: Item) =>
  item.kind === 'divider' ? 'divider' : `s${item.station.id}`;

function toItems(stations: Station[]): Item[] {
  const active = stations
    .filter(s => !s.hidden)
    .map(station => ({kind: 'station', station} as Item));
  const hidden = stations
    .filter(s => s.hidden)
    .map(station => ({kind: 'station', station} as Item));
  return [...active, DIVIDER, ...hidden];
}

// Everything above the divider is active, everything below hidden.
export function splitLayout(items: Item[]): {
  active: number[];
  hidden: number[];
} {
  const at = items.findIndex(i => i.kind === 'divider');
  const ids = (list: Item[]) =>
    list.flatMap(i => (i.kind === 'station' ? [i.station.id] : []));
  return {active: ids(items.slice(0, at)), hidden: ids(items.slice(at + 1))};
}

export function moveItem<T>(list: T[], from: number, to: number): T[] {
  const next = list.slice();
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  return next;
}

export function StationLayoutSettings({
  onDragChange,
}: {
  // Lets the parent ScrollView stop scrolling while a row is held, so the
  // drag isn't stolen by (or fighting) a scroll.
  onDragChange?: (dragging: boolean) => void;
}): React.JSX.Element {
  const theme = useTheme();
  const styles = makeStyles(theme);

  const [items, setItems] = useState<Item[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [drag, setDrag] = useState<{from: number; dy: number} | null>(null);
  // Mirrors for the PanResponder handlers below, which are created once per
  // row and would otherwise see the values from that first render.
  const itemsRef = useRef<Item[] | null>(null);
  itemsRef.current = items;
  const dragRef = useRef(drag);
  dragRef.current = drag;

  const load = useCallback(() => {
    listStations()
      .then(s => {
        setItems(toItems(s));
        setError(null);
      })
      .catch(err => setError(String(err)));
  }, []);

  useEffect(load, [load]);

  const targetFor = (from: number, dy: number, count: number) =>
    Math.max(0, Math.min(count - 1, from + Math.round(dy / ROW_HEIGHT)));

  const startDrag = useCallback(
    (key: string) => {
      const from = itemsRef.current?.findIndex(i => itemKey(i) === key) ?? -1;
      if (from < 0) return;
      setDrag({from, dy: 0});
      onDragChange?.(true);
    },
    [onDragChange],
  );

  const moveDrag = useCallback((dy: number) => {
    setDrag(d => (d ? {...d, dy} : d));
  }, []);

  const endDrag = useCallback(() => {
    const d = dragRef.current;
    const current = itemsRef.current;
    setDrag(null);
    onDragChange?.(false);
    if (!d || !current) return;
    const to = targetFor(d.from, d.dy, current.length);
    if (to === d.from) return;
    const next = moveItem(current, d.from, to);
    setItems(next);
    const {active, hidden} = splitLayout(next);
    updateStationLayout(active, hidden).catch(err => {
      setError(`Couldn't save: ${String(err)}`);
      setItems(current);
    });
  }, [onDragChange]);

  if (items === null) {
    return error ? (
      <Text style={styles.error}>{error}</Text>
    ) : (
      <ActivityIndicator color={theme.text} />
    );
  }

  const target = drag ? targetFor(drag.from, drag.dy, items.length) : -1;
  // While dragging, the rows between the held one and where it would land
  // slide one slot over to open a gap — a live preview of the drop.
  const shiftFor = (index: number) => {
    if (!drag || index === drag.from) return 0;
    if (drag.from < target && index > drag.from && index <= target)
      return -ROW_HEIGHT;
    if (drag.from > target && index >= target && index < drag.from)
      return ROW_HEIGHT;
    return 0;
  };
  const hiddenCount =
    items.length - 1 - items.findIndex(i => i.kind === 'divider');

  return (
    <View>
      <Text style={styles.hint}>
        Drag a station by its handle to reorder it. Stations below "Hidden" stay
        configured but don't show on the home list or drawer, and are skipped
        when swiping between stations.
      </Text>
      {error && <Text style={styles.error}>{error}</Text>}

      <Text style={styles.sectionTitle}>Active</Text>
      <View>
        {items.map((item, index) => {
          const dragging = drag?.from === index;
          const translateY = dragging ? drag!.dy : shiftFor(index);
          if (item.kind === 'divider') {
            return (
              <View
                key="divider"
                style={[styles.dividerRow, {transform: [{translateY}]}]}>
                <Text style={styles.sectionTitle}>Hidden</Text>
              </View>
            );
          }
          return (
            <StationRow
              key={itemKey(item)}
              station={item.station}
              dragging={dragging}
              translateY={translateY}
              onStart={() => startDrag(itemKey(item))}
              onMove={moveDrag}
              onEnd={endDrag}
              styles={styles}
              theme={theme}
            />
          );
        })}
      </View>
      {hiddenCount === 0 && (
        <Text style={styles.hint}>Drag a station here to hide it.</Text>
      )}
    </View>
  );
}

function StationRow({
  station,
  dragging,
  translateY,
  onStart,
  onMove,
  onEnd,
  styles,
  theme,
}: {
  station: Station;
  dragging: boolean;
  translateY: number;
  onStart: () => void;
  onMove: (dy: number) => void;
  onEnd: () => void;
  styles: ReturnType<typeof makeStyles>;
  theme: Theme;
}): React.JSX.Element {
  // The responder is created once, so it calls through a ref to always reach
  // the latest callbacks rather than the ones from the first render.
  const handlers = useRef({onStart, onMove, onEnd});
  handlers.current = {onStart, onMove, onEnd};

  const panResponder = useRef(
    PanResponder.create({
      onStartShouldSetPanResponder: () => true,
      onMoveShouldSetPanResponder: () => true,
      onPanResponderGrant: () => handlers.current.onStart(),
      onPanResponderMove: (_evt, gesture) =>
        handlers.current.onMove(gesture.dy),
      onPanResponderRelease: () => handlers.current.onEnd(),
      // Torn away (e.g. by the OS) — still settle the drop where it is
      // rather than leaving the row floating.
      onPanResponderTerminate: () => handlers.current.onEnd(),
      onPanResponderTerminationRequest: () => false,
    }),
  ).current;

  return (
    <View
      style={[
        styles.row,
        {borderLeftColor: station.color, transform: [{translateY}]},
        dragging && styles.rowDragging,
      ]}>
      <View
        {...panResponder.panHandlers}
        style={styles.handle}
        accessibilityLabel={`Drag ${station.name}`}>
        <Icon name="drag-handle" size={18} color={theme.textMuted} />
      </View>
      <View style={styles.rowText}>
        <Text style={styles.name} numberOfLines={1}>
          {station.name}
        </Text>
        <Text style={styles.sub} numberOfLines={1}>
          {station.model_id}
        </Text>
      </View>
    </View>
  );
}

function makeStyles(theme: Theme) {
  return StyleSheet.create({
    hint: {
      fontSize: 12,
      color: theme.textMuted,
      marginBottom: 12,
    },
    error: {
      fontSize: 13,
      color: theme.danger,
      marginBottom: 12,
    },
    sectionTitle: {
      fontSize: 13,
      fontWeight: '700',
      color: theme.textMuted,
      textTransform: 'uppercase',
      letterSpacing: 0.5,
      marginBottom: 6,
    },
    dividerRow: {
      height: ROW_HEIGHT,
      justifyContent: 'flex-end',
      borderTopWidth: 1,
      borderTopColor: theme.border,
    },
    row: {
      height: ROW_HEIGHT - 6,
      marginBottom: 6,
      flexDirection: 'row',
      alignItems: 'center',
      borderRadius: 8,
      borderLeftWidth: 4,
      backgroundColor: theme.surface,
      borderWidth: 1,
      borderColor: theme.border,
    },
    rowDragging: {
      zIndex: 10,
      elevation: 6,
      shadowColor: '#000',
      shadowOpacity: 0.25,
      shadowRadius: 6,
      shadowOffset: {width: 0, height: 2},
      borderColor: theme.primary,
    },
    handle: {
      width: 44,
      height: '100%',
      alignItems: 'center',
      justifyContent: 'center',
    },
    rowText: {
      flex: 1,
      minWidth: 0,
      paddingRight: 12,
    },
    name: {
      fontSize: 15,
      fontWeight: '600',
      color: theme.text,
    },
    sub: {
      fontSize: 12,
      color: theme.textMuted,
      marginTop: 2,
    },
  });
}
