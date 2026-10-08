import React, {useEffect, useState} from 'react';
import {AccessibilityInfo, ActivityIndicator, Animated, Easing, Platform, StyleSheet, Text, View} from 'react-native';
import {useTheme} from '../theme';

export const DIG_WORDS = [
  'Burrowing',
  'Tunneling',
  'Excavating',
  'Unearthing',
  'Delving',
  'Spelunking',
  'Trenching',
  'Shoveling',
  'Scooping',
  'Rummaging',
  'Digging',
  'Pawing',
  'Clawing',
  'Scraping',
  'Sifting',
  'Tilling',
  'Churning',
  'Boring',
  'Drilling',
  'Mining',
  'Quarrying',
  'Dredging',
  'Grubbing',
  'Rooting',
  'Prospecting',
  'Spading',
  'Hollowing',
  'Undermining',
  'Subterraneaning',
  'Bedrocking',
];

const WORD_INTERVAL_MS = 2500;
const DOTS_INTERVAL_MS = 400;

// Minecraft-style dirt chunks kicked up beside the word: square, unrotated,
// a few browns. Each particle loops its own arc (dx sideways, up by `peak`,
// then falls past its start) on a staggered offset so they never move in
// lockstep.
const DIRT_COLORS = ['#8B5A2B', '#6B4423', '#A0522D', '#5C3A1E'];
const PARTICLES = [
  {dx: -10, peak: 9, size: 3, duration: 700, delay: 0},
  {dx: 8, peak: 12, size: 4, duration: 800, delay: 120},
  {dx: -4, peak: 14, size: 3, duration: 650, delay: 260},
  {dx: 12, peak: 7, size: 2, duration: 600, delay: 380},
  {dx: -13, peak: 5, size: 2, duration: 550, delay: 470},
  {dx: 3, peak: 10, size: 4, duration: 750, delay: 560},
];
const BURST_W = 30;
const BURST_H = 18;
const FALL = 4;

function randomWord(exclude?: string): string {
  let word = exclude;
  while (word === exclude) {
    word = DIG_WORDS[Math.floor(Math.random() * DIG_WORDS.length)];
  }
  return word!;
}

function DirtParticle({p, color}: {p: (typeof PARTICLES)[number]; color: string}): React.JSX.Element {
  const [t] = useState(() => new Animated.Value(0));

  useEffect(() => {
    const anim = Animated.loop(
      Animated.sequence([
        Animated.delay(p.delay),
        Animated.timing(t, {
          toValue: 1,
          duration: p.duration,
          easing: Easing.linear,
          useNativeDriver: Platform.OS !== 'web',
        }),
      ]),
    );
    anim.start();
    return () => anim.stop();
  }, [p, t]);

  return (
    <Animated.View
      style={[
        styles.particle,
        {
          width: p.size,
          height: p.size,
          backgroundColor: color,
          opacity: t.interpolate({inputRange: [0, 0.7, 1], outputRange: [1, 1, 0]}),
          transform: [
            {translateX: t.interpolate({inputRange: [0, 1], outputRange: [0, p.dx]})},
            {
              translateY: t.interpolate({
                inputRange: [0, 0.4, 1],
                outputRange: [0, -p.peak, FALL],
                easing: Easing.out(Easing.quad),
              }),
            },
          ],
        },
      ]}
    />
  );
}

// Claude-style "busy" line: a spinner plus a rotating digging word, shown at
// the tail of an in-flight assistant reply, with dirt flying off the end.
export function DiggingSpinner(): React.JSX.Element {
  const theme = useTheme();
  const [word, setWord] = useState(() => randomWord());
  const [reduceMotion, setReduceMotion] = useState(false);

  const [dots, setDots] = useState(0);

  useEffect(() => {
    const timer = setInterval(() => setWord(w => randomWord(w)), WORD_INTERVAL_MS);
    return () => clearInterval(timer);
  }, []);

  useEffect(() => {
    const timer = setInterval(() => setDots(d => (d + 1) % 4), DOTS_INTERVAL_MS);
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

  return (
    <View style={styles.row}>
      <ActivityIndicator size="small" color={theme.textMuted} />
      <Text style={[styles.word, {color: theme.textMuted}]}>
        {word}
        {'.'.repeat(dots)}
        {/* Invisible remainder keeps the width fixed so the dirt doesn't shift. */}
        <Text style={styles.hiddenDots}>{'.'.repeat(3 - dots)}</Text>
      </Text>
      {!reduceMotion && (
        <View style={styles.burst} pointerEvents="none">
          {PARTICLES.map((p, i) => (
            <DirtParticle key={i} p={p} color={DIRT_COLORS[i % DIRT_COLORS.length]} />
          ))}
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
  },
  word: {
    fontSize: 12,
    fontStyle: 'italic',
  },
  hiddenDots: {
    opacity: 0,
  },
  burst: {
    width: BURST_W,
    height: BURST_H,
  },
  particle: {
    position: 'absolute',
    left: BURST_W / 2,
    bottom: FALL,
  },
});
