import React, {useEffect, useMemo, useState} from 'react';
import {AccessibilityInfo, Animated, Easing, Platform, StyleSheet, Text, View} from 'react-native';
import {useTheme} from '../theme';

// Mirrored by the TUI's digWords (tui/internal/ui/dig.go) — keep the two in
// step. Its length must stay coprime with 7 (the TUI walks it with stride 7).
export const DIG_WORDS = [
  'Digging',
  'Mining',
  'Grinding',
  'Drilling',
  'Tunneling',
  'Burrowing',
  'Excavating',
  'Chiseling',
  'Quarrying',
  'Prospecting',
  'Unearthing',
  'Delving',
  'Shoveling',
  'Boring',
  'Dredging',
  'Spelunking',
];

const WORD_INTERVAL_MS = 2500;

// A 3x3 patch of Minecraft-style dirt blocks. One block at a time gets dug
// out (dims, then slowly refills), walking the grid in a scattered order,
// and each dig kicks a few chunks of dirt up off that block.
const DIRT_COLORS = ['#8B5A2B', '#6B4423', '#A0522D', '#5C3A1E'];
const GRID = 3;
const CELL = 4;
const GAP = 1;
const GRID_PX = GRID * CELL + (GRID - 1) * GAP;
const DIG_ORDER = [4, 0, 7, 2, 5, 8, 1, 6, 3];
const STEP_MS = 160;
const PERIOD_MS = DIG_ORDER.length * STEP_MS;
const DIG_MS = 90;
const REFILL_MS = 520;
const FLY_MS = 480;
// Per dig: a couple of chunks thrown sideways (dx) and up (peak).
const CHUNKS = [
  {dx: -5, peak: 7, size: 2},
  {dx: 4, peak: 9, size: 2},
];

const useNative = Platform.OS !== 'web';

function randomWord(exclude?: string): string {
  let word = exclude;
  while (word === exclude) {
    word = DIG_WORDS[Math.floor(Math.random() * DIG_WORDS.length)];
  }
  return word!;
}

// The fraction of the cycle each phase of a dig takes, from the moment the
// block is hit.
const DUG = DIG_MS / PERIOD_MS;
const REFILLED = (DIG_MS + REFILL_MS) / PERIOD_MS;
const FLOWN = FLY_MS / PERIOD_MS;

// `clock` runs 0→1 once per cycle, shared by every block so they never drift
// apart; each block shifts it so that its own dig lands at local time 0.
function DirtBlock({
  index,
  slot,
  clock,
}: {
  index: number;
  slot: number;
  clock: Animated.Value | null;
}): React.JSX.Element {
  const col = index % GRID;
  const row = Math.floor(index / GRID);
  const left = col * (CELL + GAP);
  const top = row * (CELL + GAP);
  const color = DIRT_COLORS[(index * 3) % DIRT_COLORS.length];
  const t = useMemo(
    () => (clock ? Animated.modulo(Animated.add(clock, 1 - slot / DIG_ORDER.length), 1) : null),
    [clock, slot],
  );

  if (!t) return <View style={[styles.cell, {left, top, backgroundColor: color}]} />;
  return (
    <>
      <Animated.View
        style={[
          styles.cell,
          {
            left,
            top,
            backgroundColor: color,
            opacity: t.interpolate({
              inputRange: [0, DUG, REFILLED, 1],
              outputRange: [1, 0.12, 1, 1],
            }),
          },
        ]}
      />
      {CHUNKS.map((c, i) => (
        <Animated.View
          key={i}
          style={[
            styles.chunk,
            {
              left: left + (CELL - c.size) / 2,
              top: top + (CELL - c.size) / 2,
              width: c.size,
              height: c.size,
              backgroundColor: DIRT_COLORS[(index + i + 1) % DIRT_COLORS.length],
              opacity: t.interpolate({
                inputRange: [0, FLOWN * 0.7, FLOWN, 1],
                outputRange: [1, 1, 0, 0],
              }),
              transform: [
                {
                  translateX: t.interpolate({
                    inputRange: [0, FLOWN, 1],
                    outputRange: [0, c.dx, c.dx],
                  }),
                },
                {
                  translateY: t.interpolate({
                    inputRange: [0, FLOWN * 0.4, FLOWN, 1],
                    outputRange: [0, -c.peak, 2, 2],
                  }),
                },
              ],
            },
          ]}
        />
      ))}
    </>
  );
}

// Shown at the tail of an in-flight reply while the model is thinking: a
// little patch of dirt being dug out, and a rotating digging word.
export function DiggingSpinner(): React.JSX.Element {
  const theme = useTheme();
  const [word, setWord] = useState(() => randomWord());
  const [reduceMotion, setReduceMotion] = useState(false);
  const [clock] = useState(() => new Animated.Value(0));

  useEffect(() => {
    const timer = setInterval(() => setWord(w => randomWord(w)), WORD_INTERVAL_MS);
    return () => clearInterval(timer);
  }, []);

  useEffect(() => {
    let alive = true;
    // Promise.resolve: never trust a platform API to hand back a promise.
    Promise.resolve(AccessibilityInfo.isReduceMotionEnabled?.())
      .then(v => alive && setReduceMotion(!!v))
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    if (reduceMotion) return;
    const anim = Animated.loop(
      Animated.timing(clock, {
        toValue: 1,
        duration: PERIOD_MS,
        easing: Easing.linear,
        useNativeDriver: useNative,
      }),
    );
    anim.start();
    return () => anim.stop();
  }, [clock, reduceMotion]);

  return (
    <View style={styles.row} accessibilityLabel={word}>
      <View style={styles.grid} pointerEvents="none">
        {DIG_ORDER.map((cellIndex, slot) => (
          <DirtBlock key={cellIndex} index={cellIndex} slot={slot} clock={reduceMotion ? null : clock} />
        ))}
      </View>
      <Text style={[styles.word, {color: theme.textMuted}]}>{word}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
    paddingTop: 4,
  },
  grid: {
    width: GRID_PX,
    height: GRID_PX,
  },
  cell: {
    position: 'absolute',
    width: CELL,
    height: CELL,
  },
  chunk: {
    position: 'absolute',
  },
  word: {
    fontSize: 12,
    fontStyle: 'italic',
  },
});
