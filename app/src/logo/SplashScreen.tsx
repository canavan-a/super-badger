import React, {useEffect, useMemo, useRef, useState} from 'react';
import {AccessibilityInfo, Animated, Easing, Platform, Pressable, StyleSheet, View, useWindowDimensions} from 'react-native';

import type {Theme} from '../theme';
import {BADGER_H, BADGER_STRIPS, LOGO_H, LOGO_W, PIXEL_W, SHINE_STRIPS, ToneLayer, WORD_H, WORD_STRIPS, WORD_TOP} from './LogoArt';
import {LOGO, ToneRuns} from './logoData';
import {shineColor, toneColor} from './logoTheme';

// The launch screen: the same badger and wordmark as the terminal's title
// screen, recolored for the current theme. The badger settles into place, the
// letters rise in one after another, then a shine sweeps across the whole logo
// and catches the badger's eye on the way.
//
// Everything animated is an opacity or a translate/scale on the native driver,
// and nothing else: no skew, no clipping, no layers moving against each other.
// (The shine used to be a slanted, clipped window sliding over a counter-moving
// copy of the logo. React Native on Android drops skew from view transforms
// anyway, so it could never look right there, and it was the riskiest part of
// the screen.) The shine is now a row of thin strips of a lightened copy of the
// logo, each fading in and out as the sweep passes it.

/** Timeline, in milliseconds. */
export const SPLASH_TIMING = {
  badgerIn: 720,
  letterStart: 300,
  letterStagger: 42,
  letterDur: 380,
  shineStart: 1150,
  shineDur: 950,
  hold: 2350, // when the exit fade begins
  exit: 350,
};

/** How much of the way toward the shine target the highlight colors go. */
const SHINE_STRENGTH = 0.7;

/** How bright the highlight gets on the strip the sweep is over (its opacity). */
const SHINE_PEAK = 0.55;

/** The wordmark's part of the sweep trails the badger's a little, which reads as a diagonal. */
const WORD_LAG = 0.05;

/** Total running time. */
export const SPLASH_TOTAL = SPLASH_TIMING.hold + SPLASH_TIMING.exit;

/**
 * The shine's progress (0..1) at which the sweep is over strip i of n: the
 * strip fades up from nothing, peaks, and fades away. Strictly increasing, as
 * an interpolation's input range must be.
 */
export function pulseRange(i: number, n: number, lag: number): [number, number, number] {
  const c = 0.06 + (0.88 * i) / (n - 1) + lag;
  return [Math.max(0, c - 0.13), c, Math.min(1, c + 0.13)];
}

/** The shine's progress at which the sweep is over the badger's eye. */
export function eyePassAt(): number {
  const c = 0.06 + 0.88 * (LOGO.eye.cx / LOGO.width);
  return c;
}

interface Props {
  theme: Theme;
  /** called when the splash has finished (or was tapped away) */
  onDone: () => void;
  /** skip the motion: hold the logo briefly, then fade */
  reduceMotion?: boolean;
}

export function SplashScreen({theme, onDone, reduceMotion}: Props): React.JSX.Element {
  const {width: screenW} = useWindowDimensions();
  const W = Math.min(screenW * 0.9, 480);
  const scale = W / LOGO_W;
  const H = LOGO_H * scale;
  const useNative = Platform.OS !== 'web';

  const [reduce, setReduce] = useState(reduceMotion ?? false);
  useEffect(() => {
    if (reduceMotion !== undefined) return;
    let alive = true;
    // Promise.resolve: never trust a platform API to hand back a promise.
    Promise.resolve(AccessibilityInfo.isReduceMotionEnabled?.())
      .then(v => alive && setReduce(!!v))
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [reduceMotion]);

  // Created once. (useRef({...}) would build a fresh set of Animated.Values on
  // every render and throw all but the first away.)
  const values = useRef<{
    badger: Animated.Value;
    letters: Animated.Value[];
    shine: Animated.Value;
    exit: Animated.Value;
  } | null>(null);
  if (!values.current) {
    values.current = {
      badger: new Animated.Value(reduce ? 1 : 0),
      letters: LOGO.word.slices.map(() => new Animated.Value(reduce ? 1 : 0)),
      shine: new Animated.Value(0),
      exit: new Animated.Value(1),
    };
  }
  const v = values.current;

  const finished = useRef(false);
  const skipping = useRef(false);
  const running = useRef<Animated.CompositeAnimation | null>(null);
  const done = () => {
    if (finished.current) return;
    finished.current = true;
    onDone();
  };
  // A tap fades the splash out rather than unmounting it on the spot: pulling
  // views out from under natively-driven animations that are still running is
  // a classic way to crash React Native on Android.
  const skip = () => {
    if (finished.current || skipping.current) return;
    skipping.current = true;
    running.current?.stop();
    Animated.timing(v.exit, {toValue: 0, duration: 160, easing: Easing.linear, useNativeDriver: useNative}).start(done);
  };

  useEffect(() => {
    const T = SPLASH_TIMING;
    const timing = (val: Animated.Value, to: number, duration: number, delay: number, easing = Easing.out(Easing.cubic)) =>
      Animated.timing(val, {toValue: to, duration, delay, easing, useNativeDriver: useNative});

    const animation = reduce
      ? Animated.sequence([Animated.delay(600), timing(v.exit, 0, 200, 0, Easing.linear)])
      : Animated.parallel([
          timing(v.badger, 1, T.badgerIn, 0),
          ...v.letters.map((l, i) => timing(l, 1, T.letterDur, T.letterStart + i * T.letterStagger)),
          timing(v.shine, 1, T.shineDur, T.shineStart, Easing.inOut(Easing.quad)),
          Animated.sequence([Animated.delay(T.hold), timing(v.exit, 0, T.exit, 0, Easing.in(Easing.quad))]),
        ]);
    running.current = animation;
    animation.start(r => r.finished && done());
    return () => {
      running.current = null;
      animation.stop();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reduce]);

  // colors for this theme
  const base = useMemo(() => (t: ToneRuns) => toneColor(theme, t.u, t.w), [theme]);
  const glint = useMemo(() => (t: ToneRuns) => shineColor(toneColor(theme, t.u, t.w), theme, SHINE_STRENGTH), [theme]);

  // The shine's opacities, built once so their native nodes aren't recreated.
  const fades = useMemo(() => {
    const pulse = (range: [number, number, number], peak: number) =>
      v.shine.interpolate({inputRange: range, outputRange: [0, peak, 0], extrapolate: 'clamp'});
    const at = eyePassAt();
    return {
      badger: BADGER_STRIPS.map((_, i) => pulse(pulseRange(i, SHINE_STRIPS, 0), SHINE_PEAK)),
      word: WORD_STRIPS.map((_, i) => pulse(pulseRange(i, SHINE_STRIPS, WORD_LAG), SHINE_PEAK)),
      eye: pulse([Math.max(0, at - 0.09), at, Math.min(1, at + 0.13)], 1),
    };
  }, [v]);

  const badgerH = BADGER_H * scale;
  const wordTop = WORD_TOP * scale;
  const wordH = WORD_H * scale;

  const strips = (list: typeof BADGER_STRIPS, opacities: Animated.AnimatedInterpolation<number>[], top: number, height: number, regionH: number, key: string) =>
    list.map((s, i) =>
      s.tones.length === 0 ? null : (
        <Animated.View
          key={`${key}${i}`}
          pointerEvents="none"
          style={[
            styles.abs,
            {left: s.x0 * PIXEL_W * scale, top, width: (s.x1 - s.x0) * PIXEL_W * scale, height, opacity: opacities[i]},
          ]}>
          <ToneLayer
            tones={s.tones}
            fill={glint}
            view={[s.x0 * PIXEL_W, 0, (s.x1 - s.x0) * PIXEL_W, regionH]}
            width={(s.x1 - s.x0) * PIXEL_W * scale}
            height={height}
          />
        </Animated.View>
      ),
    );

  return (
    <Animated.View style={[StyleSheet.absoluteFill, {backgroundColor: theme.bg, opacity: v.exit}]} accessible accessibilityLabel="Super Badger">
      <Pressable style={styles.center} onPress={skip} accessibilityRole="button" accessibilityLabel="Skip intro">
        <View style={{width: W, height: H}}>
          <Animated.View
            style={[
              styles.abs,
              {width: W, height: badgerH, opacity: v.badger},
              {
                transform: [
                  {translateY: v.badger.interpolate({inputRange: [0, 1], outputRange: [16, 0]})},
                  {scale: v.badger.interpolate({inputRange: [0, 1], outputRange: [0.94, 1]})},
                ],
              },
            ]}>
            <ToneLayer tones={LOGO.badger.tones} fill={base} view={[0, 0, LOGO_W, BADGER_H]} width={W} height={badgerH} />
            <ToneLayer tones={LOGO.eye.tones} fill={base} view={[0, 0, LOGO_W, BADGER_H]} width={W} height={badgerH} />
            <Animated.View style={[styles.abs, {width: W, height: badgerH, opacity: fades.eye}]}>
              <ToneLayer tones={LOGO.eye.tones} fill={glint} view={[0, 0, LOGO_W, BADGER_H]} width={W} height={badgerH} />
            </Animated.View>
          </Animated.View>

          {LOGO.word.slices.map((s, i) => (
            <Animated.View
              key={s.x0}
              style={[
                styles.abs,
                {
                  left: s.x0 * PIXEL_W * scale,
                  top: wordTop,
                  width: (s.x1 - s.x0) * PIXEL_W * scale,
                  height: wordH,
                  opacity: v.letters[i],
                  transform: [{translateY: v.letters[i].interpolate({inputRange: [0, 1], outputRange: [14, 0]})}],
                },
              ]}>
              <ToneLayer
                tones={s.tones}
                fill={base}
                view={[s.x0 * PIXEL_W, 0, (s.x1 - s.x0) * PIXEL_W, WORD_H]}
                width={(s.x1 - s.x0) * PIXEL_W * scale}
                height={wordH}
              />
            </Animated.View>
          ))}

          {!reduce && (
            <View style={[styles.abs, {width: W, height: H}]} pointerEvents="none">
              {strips(BADGER_STRIPS, fades.badger, 0, badgerH, BADGER_H, 'b')}
              {strips(WORD_STRIPS, fades.word, wordTop, wordH, WORD_H, 'w')}
            </View>
          )}
        </View>
      </Pressable>
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  center: {flex: 1, alignItems: 'center', justifyContent: 'center'},
  abs: {position: 'absolute', top: 0, left: 0},
});
