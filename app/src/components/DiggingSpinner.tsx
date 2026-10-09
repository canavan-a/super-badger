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

// A drill bit pointing down, drawn in the word's text color: the chuck on
// top, a fluted bit whose flutes scroll upward so it reads as spinning, and
// a point at the bottom that kicks chunks of dirt up and out to either side.
const DIRT_COLORS = ['#8B5A2B', '#6B4423', '#A0522D', '#5C3A1E'];
const BOX_W = 14;
const BOX_H = 16;
const CHUCK_W = 8;
const CHUCK_H = 4;
const BIT_W = 4;
const BIT_H = 8;
const TIP_H = 4;
const BIT_LEFT = (BOX_W - BIT_W) / 2;
// Flutes: diagonal stripes PITCH apart, scrolled up two pitches per cycle (a
// whole number of pitches, so the loop is seamless).
const PITCH = 3;
const FLUTES = [-1, 0, 1, 2, 3, 4, 5].map(i => i * PITCH);
const PERIOD_MS = 700;
// The fraction of the cycle a chunk spends in the air.
const FLY = 0.55;
// Chunks thrown off the tip, each launched at its own point (`at`) in the
// cycle: sideways (dx) and up (peak).
const CHUNKS = [
  {at: 0, dx: -6, peak: 7, size: 2},
  {at: 0.25, dx: 5, peak: 9, size: 1.5},
  {at: 0.5, dx: -4, peak: 10, size: 1.5},
  {at: 0.75, dx: 6, peak: 6, size: 2},
];

const useNative = Platform.OS !== 'web';

function randomWord(exclude?: string): string {
  let word = exclude;
  while (word === exclude) {
    word = DIG_WORDS[Math.floor(Math.random() * DIG_WORDS.length)];
  }
  return word!;
}

// `clock` runs 0→1 once per cycle, shared by every chunk so they never drift
// apart; each chunk shifts it so that its own launch lands at local time 0.
function Chunk({
  chunk,
  index,
  clock,
}: {
  chunk: (typeof CHUNKS)[number];
  index: number;
  clock: Animated.Value;
}): React.JSX.Element {
  const t = useMemo(() => Animated.modulo(Animated.add(clock, 1 - chunk.at), 1), [clock, chunk.at]);
  return (
    <Animated.View
      style={[
        styles.chunk,
        {
          left: (BOX_W - chunk.size) / 2,
          top: BOX_H - chunk.size - 1,
          width: chunk.size,
          height: chunk.size,
          backgroundColor: DIRT_COLORS[index % DIRT_COLORS.length],
          opacity: t.interpolate({
            inputRange: [0, FLY * 0.7, FLY, 1],
            outputRange: [1, 1, 0, 0],
          }),
          transform: [
            {
              translateX: t.interpolate({
                inputRange: [0, FLY, 1],
                outputRange: [0, chunk.dx, chunk.dx],
              }),
            },
            {
              translateY: t.interpolate({
                inputRange: [0, FLY * 0.4, FLY, 1],
                outputRange: [0, -chunk.peak, 1, 1],
              }),
            },
          ],
        },
      ]}
    />
  );
}

function Drill({color, clock}: {color: string; clock: Animated.Value | null}): React.JSX.Element {
  const scroll = useMemo(
    () => (clock ? clock.interpolate({inputRange: [0, 1], outputRange: [0, -2 * PITCH]}) : 0),
    [clock],
  );
  // A slight bob, as if bearing down into the ground.
  const bob = useMemo(
    () => (clock ? clock.interpolate({inputRange: [0, 0.5, 1], outputRange: [0, 0.75, 0]}) : 0),
    [clock],
  );
  return (
    <View style={styles.box} pointerEvents="none">
      <Animated.View style={[StyleSheet.absoluteFill, {transform: [{translateY: bob}]}]}>
        <View style={[styles.chuck, {backgroundColor: color}]} />
        <View style={styles.bit}>
          <View style={[styles.bitBody, {backgroundColor: color}]} />
          <Animated.View style={[StyleSheet.absoluteFill, {transform: [{translateY: scroll}]}]}>
            {FLUTES.map(y => (
              <View key={y} style={[styles.flute, {top: y, backgroundColor: color}]} />
            ))}
          </Animated.View>
        </View>
        <View style={[styles.tip, {borderTopColor: color}]} />
      </Animated.View>
      {clock && CHUNKS.map((c, i) => <Chunk key={i} chunk={c} index={i} clock={clock} />)}
    </View>
  );
}

// Shown at the tail of an in-flight reply while the model is thinking: a
// little drill boring down, and a rotating digging word.
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
      <Drill color={theme.textMuted} clock={reduceMotion ? null : clock} />
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
  box: {
    width: BOX_W,
    height: BOX_H,
  },
  chuck: {
    position: 'absolute',
    left: (BOX_W - CHUCK_W) / 2,
    top: 0,
    width: CHUCK_W,
    height: CHUCK_H,
    borderRadius: 1,
  },
  bit: {
    position: 'absolute',
    left: BIT_LEFT,
    top: CHUCK_H,
    width: BIT_W,
    height: BIT_H,
    overflow: 'hidden',
  },
  bitBody: {
    ...StyleSheet.absoluteFillObject,
    opacity: 0.4,
  },
  flute: {
    position: 'absolute',
    left: -2,
    width: BIT_W + 4,
    height: 1.25,
    transform: [{rotate: '-30deg'}],
  },
  tip: {
    position: 'absolute',
    left: BIT_LEFT,
    top: CHUCK_H + BIT_H,
    width: 0,
    height: 0,
    borderLeftWidth: BIT_W / 2,
    borderRightWidth: BIT_W / 2,
    borderTopWidth: TIP_H,
    borderLeftColor: 'transparent',
    borderRightColor: 'transparent',
  },
  chunk: {
    position: 'absolute',
  },
  word: {
    fontSize: 12,
    fontStyle: 'italic',
  },
});
