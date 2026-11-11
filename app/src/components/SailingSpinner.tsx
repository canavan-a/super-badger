import React, {useEffect, useState} from 'react';
import {AccessibilityInfo, StyleSheet, Text, View} from 'react-native';
import {useTheme} from '../theme';
import Svg, {Circle, G, Line, Polyline, Rect} from 'react-native-svg';

// Mirrored by the TUI's sailWords (tui/internal/ui/sail.go) — keep the two in
// step. Its length must stay coprime with 7 (the TUI walks it with stride 7).
export const SAIL_WORDS = [
  'Sailing',
  'Charting',
  'Navigating',
  'Tacking',
  'Reaching',
  'Trimming',
  'Plotting',
  'Steering',
  'Sounding',
  'Reckoning',
  'Surveying',
  'Piloting',
  'Beating',
  'Bearing',
  'Heading',
  'Running',
  'Plundering',
  'Hoisting',
  'Parleying',
  'Swashbuckling',
];

const WORD_INTERVAL_MS = 2500;

// A course being plotted on a chart, drawn in the word's text color: a
// diamond sails a repeating "long and short" course (a long climbing leg, a
// short sharp drop), laying a line down behind it with a dot at each
// waypoint. The chart stays put while the diamond crosses the middle of it;
// each time it gets a period ahead, the chart slides over one period. Since
// the slide is exactly one period, every screen looks the same as the last,
// so the whole animation is a loop of a single period.
const BOX = 20;
// One period of the course, starting at x = 0; it closes onto the next
// period's first point at x = PER.
const COURSE: [number, number][] = [
  [0, 13],
  [7.5, 7],
];
const PER = 10;
// Chart units per second, measured along the line.
const SPEED = 8;
// The diamond crosses x START → START + PER on screen; once past START the
// chart eases over one period while it sails PAN further.
const START = 5;
const PAN = 2.5;
// Periods of course kept behind the diamond — enough to fill the left edge.
const HISTORY = 2;

const LEGS = (() => {
  const pts = [...COURSE, [PER, COURSE[0][1]] as [number, number]];
  const legs: {a: [number, number]; b: [number, number]; len: number; start: number}[] = [];
  let total = 0;
  for (let i = 1; i < pts.length; i++) {
    const len = Math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]);
    legs.push({a: pts[i - 1], b: pts[i], len, start: total});
    total += len;
  }
  return {legs, total};
})();
const PERIOD_MS = (LEGS.total / SPEED) * 1000;

const clamp = (v: number) => Math.min(1, Math.max(0, v));
const easeInOut = (x: number) => (x < 0.5 ? 2 * x * x : 1 - Math.pow(-2 * x + 2, 2) / 2);

interface Frame {
  // The drawn line and its waypoints, in chart coordinates.
  points: string;
  waypoints: [number, number][];
  // Where the diamond is, in chart coordinates.
  head: [number, number];
  // How far the chart has slid left.
  pan: number;
}

// The frame at `phase` (0–1) through one period of the loop. The diamond is
// placed HISTORY periods in, so there's always line trailing behind it.
export function courseFrame(phase: number): Frame {
  const r = clamp(phase) * LEGS.total;
  let i = LEGS.legs.findIndex(l => r <= l.start + l.len);
  if (i < 0) i = LEGS.legs.length - 1;
  const leg = LEGS.legs[i];
  const k = leg.len ? (r - leg.start) / leg.len : 0;
  const head: [number, number] = [
    leg.a[0] + (leg.b[0] - leg.a[0]) * k + HISTORY * PER,
    leg.a[1] + (leg.b[1] - leg.a[1]) * k,
  ];

  const waypoints: [number, number][] = [];
  for (let n = 0; n <= HISTORY; n++) {
    COURSE.forEach(([x, y], j) => {
      if (n < HISTORY || j <= i) waypoints.push([x + n * PER, y]);
    });
  }

  const page = Math.floor((head[0] - START) / PER);
  const pan = page * PER - PER * (1 - easeInOut(clamp((head[0] - START - page * PER) / PAN)));

  const points = [...waypoints, head].map(([x, y]) => `${x},${y}`).join(' ');
  return {points, waypoints, head, pan};
}

function randomWord(exclude?: string): string {
  let word = exclude;
  while (word === exclude) {
    word = SAIL_WORDS[Math.floor(Math.random() * SAIL_WORDS.length)];
  }
  return word!;
}

function Chart({color, phase}: {color: string; phase: number}): React.JSX.Element {
  const {points, waypoints, head, pan} = courseFrame(phase);
  // Vertical grid lines every 5 units, slid along with the chart.
  const gridX: number[] = [];
  for (let x = Math.ceil(pan / 5) * 5; x <= pan + BOX; x += 5) gridX.push(x);
  return (
    <Svg width={BOX} height={BOX} viewBox={`0 0 ${BOX} ${BOX}`}>
      <G stroke={color} strokeOpacity={0.16} strokeWidth={0.45}>
        {[5, 10, 15].map(y => (
          <Line key={`h${y}`} x1={0} y1={y} x2={BOX} y2={y} />
        ))}
        {gridX.map(x => (
          <Line key={`v${x}`} x1={x - pan} y1={0} x2={x - pan} y2={BOX} />
        ))}
      </G>
      <G transform={`translate(${-pan} 0)`}>
        <Polyline
          points={points}
          fill="none"
          stroke={color}
          strokeWidth={1}
          strokeLinejoin="round"
          strokeLinecap="round"
        />
        {waypoints.map(([x, y]) => (
          <Circle key={`${x},${y}`} cx={x} cy={y} r={0.55} fill={color} />
        ))}
      </G>
      <Rect
        x={-1.1}
        y={-1.1}
        width={2.2}
        height={2.2}
        fill={color}
        transform={`translate(${head[0] - pan} ${head[1]}) rotate(45)`}
      />
    </Svg>
  );
}

// Shown at the tail of an in-flight reply while the model is thinking: a
// course being plotted on a chart, and a rotating nautical word.
export function SailingSpinner(): React.JSX.Element {
  const theme = useTheme();
  const [word, setWord] = useState(() => randomWord());
  const [reduceMotion, setReduceMotion] = useState(false);
  const [phase, setPhase] = useState(0.4);

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

  // The track is redrawn every frame (a polyline's points can't be driven by
  // Animated), but it's one 20px SVG and only shows while a reply is thinking.
  useEffect(() => {
    if (reduceMotion) return;
    let raf = 0;
    const t0 = Date.now();
    const loop = () => {
      setPhase(((Date.now() - t0) % PERIOD_MS) / PERIOD_MS);
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(raf);
  }, [reduceMotion]);

  return (
    <View style={styles.row} accessibilityLabel={word}>
      <View style={styles.box} pointerEvents="none">
        <Chart color={theme.textMuted} phase={reduceMotion ? 0.4 : phase} />
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
  box: {
    width: BOX,
    height: BOX,
  },
  word: {
    fontSize: 12,
    fontStyle: 'italic',
  },
});
