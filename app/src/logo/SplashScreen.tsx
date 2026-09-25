import React, {useEffect, useMemo, useRef, useState} from 'react';
import {AccessibilityInfo, Animated, Easing, Platform, Pressable, StyleSheet, View, useWindowDimensions} from 'react-native';

import type {Theme} from '../theme';
import {BADGER_H, FullLogo, LOGO_H, LOGO_W, PIXEL_H, PIXEL_W, ToneLayer, WORD_H, WORD_TOP} from './LogoArt';
import {LOGO, ToneRuns} from './logoData';
import {shineColor, toneColor} from './logoTheme';

// The launch screen: the same badger and wordmark as the terminal's title
// screen, recolored for the current theme. The badger settles into place, the
// letters rise in one after another, then a slanted glint sweeps across the
// whole logo and catches the badger's eye on the way. Everything animated is a
// transform or an opacity, so it runs on the native driver and stays smooth
// while the app underneath is still loading.

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

/** Slant of the shine, in degrees. */
const SKEW = 18;

/** How much of the way toward the shine target the highlight colors go. */
const SHINE_STRENGTH = 0.7;

/** Total running time. */
export const SPLASH_TOTAL = SPLASH_TIMING.hold + SPLASH_TIMING.exit;

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

  const v = useRef({
    badger: new Animated.Value(reduce ? 1 : 0),
    letters: LOGO.word.slices.map(() => new Animated.Value(reduce ? 1 : 0)),
    shine: new Animated.Value(0),
    exit: new Animated.Value(1),
  }).current;

  const finished = useRef(false);
  const done = () => {
    if (finished.current) return;
    finished.current = true;
    onDone();
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
    animation.start(r => r.finished && done());
    return () => animation.stop();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reduce]);

  // colors for this theme
  const base = useMemo(() => (t: ToneRuns) => toneColor(theme, t.u, t.w), [theme]);
  const glint = useMemo(() => (t: ToneRuns) => shineColor(toneColor(theme, t.u, t.w), theme, SHINE_STRENGTH), [theme]);

  // the shine: a slanted band, wide and soft with a narrower brighter core
  const bandW = W * 0.24;
  const coreW = bandW * 0.38;
  const from = -bandW * 1.6;
  const to = W + bandW * 0.6;
  const tx = v.shine.interpolate({inputRange: [0, 1], outputRange: [from, to]});
  const txBack = v.shine.interpolate({inputRange: [0, 1], outputRange: [-from, -to]});

  // Where in the sweep the band's center is level with the eye, so the eye's
  // glint fires exactly as the shine passes it. The slant shifts the band
  // sideways by tan(SKEW) per unit of height from the vertical middle.
  const eyeX = LOGO.eye.cx * PIXEL_W * scale;
  const eyeY = LOGO.eye.cy * PIXEL_H * scale;
  const slantShift = Math.tan((SKEW * Math.PI) / 180) * (H / 2 - eyeY);
  const eyeAt = (eyeX - bandW / 2 - slantShift - from) / (to - from);
  const eyeGlow = v.shine.interpolate({
    inputRange: [Math.max(0, eyeAt - 0.09), eyeAt, Math.min(1, eyeAt + 0.13)],
    outputRange: [0, 1, 0],
    extrapolate: 'clamp',
  });

  const band = (width: number, opacity: number, key: string) => {
    const left = (bandW - width) / 2;
    return (
      <Animated.View
        key={key}
        pointerEvents="none"
        style={[
          styles.abs,
          {left, width, height: H, overflow: 'hidden', opacity},
          {transform: [{translateX: tx}, {skewX: `-${SKEW}deg`}]},
        ]}>
        {/* the logo, held still against the moving window */}
        <Animated.View
          style={[styles.abs, {left: -left, width: W, height: H}, {transform: [{skewX: `${SKEW}deg`}, {translateX: txBack}]}]}>
          <FullLogo fill={glint} width={W} height={H} />
        </Animated.View>
      </Animated.View>
    );
  };

  const badgerH = BADGER_H * scale;
  const wordTop = WORD_TOP * scale;
  const wordH = WORD_H * scale;

  return (
    <Animated.View style={[StyleSheet.absoluteFill, {backgroundColor: theme.bg, opacity: v.exit}]} accessible accessibilityLabel="Super Badger">
      <Pressable style={styles.center} onPress={done} accessibilityRole="button" accessibilityLabel="Skip intro">
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
            <Animated.View style={[styles.abs, {width: W, height: badgerH, opacity: eyeGlow}]}>
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
              {band(bandW, 0.34, 'wide')}
              {band(coreW, 0.6, 'core')}
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
