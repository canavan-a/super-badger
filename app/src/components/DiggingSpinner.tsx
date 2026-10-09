import React, {useEffect, useId, useMemo, useState} from 'react';
import {AccessibilityInfo, Animated, Easing, Platform, StyleSheet, Text, View} from 'react-native';
import {useTheme} from '../theme';
import Svg, {ClipPath, Defs, G, Line, Polygon, Rect} from 'react-native-svg';

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

// A cartoon drill pointing down, drawn in the word's text color: a shank and
// collar on top, then a fat cone tapering to a point, wrapped in spiral
// grooves. It spins by flipping through FRAMES copies of the cone with the
// grooves shifted a fraction of a pitch each, and the point kicks chunks of
// dirt up and out to either side.
const DIRT_COLORS = ['#8B5A2B', '#6B4423', '#A0522D', '#5C3A1E'];
const BOX_W = 16;
const BOX_H = 20;
const CONE = '2,7 14,7 8,19.5';
const PITCH = 4;
const GROOVES = [-1, 0, 1, 2, 3, 4, 5];
const FRAMES = 3;
// Each cycle runs through the frames this many times.
const SPINS = 2;
const PERIOD_MS = 700;
// The fraction of the cycle a chunk spends in the air.
const FLY = 0.55;
// Chunks thrown off the tip, each launched at its own point (`at`) in the
// cycle: sideways (dx) and up (peak).
const CHUNKS = [
  {at: 0, dx: -7, peak: 8, size: 2},
  {at: 0.25, dx: 6, peak: 10, size: 1.5},
  {at: 0.5, dx: -5, peak: 11, size: 1.5},
  {at: 0.75, dx: 7, peak: 7, size: 2},
];

const useNative = Platform.OS !== 'web';

function randomWord(exclude?: string): string {
  let word = exclude;
  while (word === exclude) {
    word = DIG_WORDS[Math.floor(Math.random() * DIG_WORDS.length)];
  }
  return word!;
}

// Opacity keyframes that show `frame` only during its own steps of the cycle,
// switching hard (no crossfade) between steps.
function frameVisibility(frame: number): {inputRange: number[]; outputRange: number[]} {
  const steps = FRAMES * SPINS;
  const inputRange: number[] = [];
  const outputRange: number[] = [];
  for (let s = 0; s < steps; s++) {
    const on = s % FRAMES === frame ? 1 : 0;
    inputRange.push(s / steps, (s + 1) / steps - 0.0001);
    outputRange.push(on, on);
  }
  return {inputRange, outputRange};
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

function DrillFrame({color, frame, clipId}: {color: string; frame: number; clipId: string}): React.JSX.Element {
  const shift = (frame * PITCH) / FRAMES;
  return (
    <Svg width={BOX_W} height={BOX_H} viewBox={`0 0 ${BOX_W} ${BOX_H}`}>
      <Defs>
        <ClipPath id={clipId}>
          <Polygon points={CONE} />
        </ClipPath>
      </Defs>
      <Rect x={5} y={0} width={6} height={3.5} rx={1} fill={color} />
      <Rect x={1.5} y={3} width={13} height={3.5} rx={1.5} fill={color} />
      <Polygon points={CONE} fill={color} fillOpacity={0.35} />
      <G clipPath={`url(#${clipId})`}>
        {GROOVES.map(k => {
          const y = 7 + k * PITCH - shift;
          return <Line key={k} x1={0} y1={y} x2={BOX_W} y2={y - 4} stroke={color} strokeWidth={1.8} />;
        })}
      </G>
      <Polygon points={CONE} fill="none" stroke={color} strokeWidth={1} strokeLinejoin="round" />
    </Svg>
  );
}

function Drill({color, clock}: {color: string; clock: Animated.Value | null}): React.JSX.Element {
  const id = useId().replace(/[^a-zA-Z0-9_-]/g, '');
  const visibility = useMemo(
    () => (clock ? Array.from({length: FRAMES}, (_, f) => clock.interpolate(frameVisibility(f))) : null),
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
        {visibility ? (
          visibility.map((opacity, f) => (
            <Animated.View key={f} style={[StyleSheet.absoluteFill, {opacity}]}>
              <DrillFrame color={color} frame={f} clipId={`drill${id}f${f}`} />
            </Animated.View>
          ))
        ) : (
          <DrillFrame color={color} frame={0} clipId={`drill${id}`} />
        )}
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
  chunk: {
    position: 'absolute',
  },
  word: {
    fontSize: 12,
    fontStyle: 'italic',
  },
});
