import React, {memo} from 'react';
import {StyleSheet} from 'react-native';
import Svg, {G, Path} from 'react-native-svg';

import {LOGO, ToneRuns} from './logoData';

// The art's sub-pixels aren't square: in the terminal a sextant is half a cell
// wide and a third of a cell tall, and a cell is about twice as tall as wide,
// so a sub-pixel is 3 wide by 4 tall. The logo keeps that shape everywhere.
export const PIXEL_W = 3;
export const PIXEL_H = 4;

/** Overall logo size in viewBox units. */
export const LOGO_W = LOGO.width * PIXEL_W;
export const BADGER_H = LOGO.badger.rows * PIXEL_H;
export const WORD_TOP = (LOGO.badger.rows + LOGO.gap) * PIXEL_H;
export const WORD_H = LOGO.word.rows * PIXEL_H;
export const LOGO_H = WORD_TOP + WORD_H;

// Adjacent rectangles overlap by a hair so no seams show between pixels when
// the logo is scaled to a fractional size.
const OVERLAP = 0.35;

const pathCache = new WeakMap<ToneRuns, string>();

/** One tone's runs as a single SVG path of rectangles. */
export function tonePath(t: ToneRuns): string {
  const cached = pathCache.get(t);
  if (cached !== undefined) return cached;
  const parts: string[] = [];
  for (let i = 0; i < t.runs.length; i += 3) {
    const x = t.runs[i] * PIXEL_W;
    const y = t.runs[i + 1] * PIXEL_H;
    const w = t.runs[i + 2] * PIXEL_W + OVERLAP;
    parts.push(`M${x} ${y}h${w}v${PIXEL_H + OVERLAP}h${-w}z`);
  }
  const d = parts.join('');
  pathCache.set(t, d);
  return d;
}

/** Every slice of the wordmark's tones merged, for drawing it in one piece. */
export const WORD_TONES: ToneRuns[] = (() => {
  const merged = new Map<string, ToneRuns>();
  for (const s of LOGO.word.slices) {
    for (const t of s.tones) {
      const key = `${t.u}/${t.w}`;
      const m = merged.get(key);
      if (m) m.runs = m.runs.concat(t.runs);
      else merged.set(key, {u: t.u, w: t.w, runs: t.runs.slice()});
    }
  }
  return Array.from(merged.values());
})();

// Every layer sits at the origin of its container and overlays its siblings.
// (Without this the SVGs are flex children and *stack*: on the web an <svg>
// in a flex column also shrinks to share the height, which squeezed the badger
// to half size and pushed the eye out of line with the shine's copy of it.)
const styles = StyleSheet.create({
  layer: {position: 'absolute', top: 0, left: 0},
});

interface LayerProps {
  tones: ToneRuns[];
  /** the fill color for a tone */
  fill: (t: ToneRuns) => string;
  /** the part of the logo to show: x, y, width, height in viewBox units */
  view: [number, number, number, number];
  /** rendered size in pixels */
  width: number;
  height: number;
}

/** A group of tones drawn as SVG. */
export const ToneLayer = memo(function ToneLayer({tones, fill, view, width, height}: LayerProps) {
  return (
    <Svg style={styles.layer} width={width} height={height} viewBox={view.join(' ')}>
      {tones.map(t => (
        <Path key={`${t.u}/${t.w}`} d={tonePath(t)} fill={fill(t)} />
      ))}
    </Svg>
  );
});

interface FullProps {
  fill: (t: ToneRuns) => string;
  width: number;
  height: number;
}

/** The whole logo (badger, eye and wordmark) in one SVG. */
export const FullLogo = memo(function FullLogo({fill, width, height}: FullProps) {
  return (
    <Svg style={styles.layer} width={width} height={height} viewBox={`0 0 ${LOGO_W} ${LOGO_H}`}>
      <G>
        {LOGO.badger.tones.map(t => (
          <Path key={`b${t.u}/${t.w}`} d={tonePath(t)} fill={fill(t)} />
        ))}
        {LOGO.eye.tones.map(t => (
          <Path key={`e${t.u}/${t.w}`} d={tonePath(t)} fill={fill(t)} />
        ))}
      </G>
      <G transform={`translate(0 ${WORD_TOP})`}>
        {WORD_TONES.map(t => (
          <Path key={`w${t.u}/${t.w}`} d={tonePath(t)} fill={fill(t)} />
        ))}
      </G>
    </Svg>
  );
});
