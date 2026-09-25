/**
 * @format
 */
import 'react-native';
import React from 'react';
import {afterEach, beforeEach, describe, expect, it, jest} from '@jest/globals';
import renderer, {act} from 'react-test-renderer';

import {SplashBoundary, SplashGate, resetSplashForTests} from '../src/logo/SplashGate';
import {SPLASH_TIMING, SPLASH_TOTAL, SplashScreen} from '../src/logo/SplashScreen';
import {settingsStore} from '../src/settings';
import {ThemeProvider, THEMES} from '../src/theme';

// The native animated module doesn't exist under Jest; without this the
// animations fall back noisily instead of running on the JS driver.
jest.mock('react-native/Libraries/Animated/NativeAnimatedHelper');

const text = (node: renderer.ReactTestInstance): string => JSON.stringify(node.props.style ?? '');

const bgOf = (tree: renderer.ReactTestRenderer): string | undefined => {
  const root = tree.root.findAll(n => n.props?.accessibilityLabel === 'Super Badger')[0];
  const flat = ([] as any[]).concat(root.props.style).flat(3);
  return flat.find(s => s && s.backgroundColor)?.backgroundColor;
};

describe('SplashScreen', () => {
  beforeEach(() => jest.useFakeTimers());
  afterEach(() => jest.useRealTimers());

  const mount = (onDone: () => void, extra: Partial<React.ComponentProps<typeof SplashScreen>> = {}, theme = THEMES.ember) => {
    let tree!: renderer.ReactTestRenderer;
    act(() => {
      tree = renderer.create(<SplashScreen theme={theme} onDone={onDone} reduceMotion={false} {...extra} />);
    });
    return tree;
  };

  it('opens on the theme\'s own page color', () => {
    for (const t of Object.values(THEMES)) {
      const tree = mount(() => {}, {}, t);
      expect(bgOf(tree)).toBe(t.bg);
      act(() => tree.unmount());
    }
  });

  it('draws the whole logo: badger, eye, eleven letters and the shine', () => {
    const tree = mount(() => {});
    // 11 letters + badger group + the shine's two bands
    const svgs = tree.root.findAll(n => (n.type as any)?.displayName === 'Svg' || (n.type as any)?.name === 'Svg');
    expect(svgs.length).toBeGreaterThanOrEqual(11 + 3);
    act(() => tree.unmount());
  });

  it('overlays every SVG layer at the origin instead of letting them stack', () => {
    // Regression: the badger and its eye were separate SVGs in a flex column,
    // so they stacked (and, on the web, shrank to share the height) rather than
    // lining up with each other and with the shine's copy of the logo.
    const tree = mount(() => {});
    const svgs = tree.root.findAll(n => typeof n.props?.viewBox === 'string' && n.props.width !== undefined);
    // findAll also sees wrapper components; keep only the outermost per SVG.
    expect(svgs.length).toBeGreaterThan(0);
    for (const n of svgs) {
      const style = Object.assign({}, ...([] as any[]).concat(n.props.style ?? {}).flat(3));
      expect({viewBox: n.props.viewBox, position: style.position, top: style.top, left: style.left}).toEqual({
        viewBox: n.props.viewBox,
        position: 'absolute',
        top: 0,
        left: 0,
      });
    }
    act(() => tree.unmount());
  });

  it('draws the badger and its eye at the same size and origin, so the eye sits on the head', () => {
    const tree = mount(() => {});
    const full = tree.root.findAll(n => typeof n.props?.viewBox === 'string' && n.props.viewBox.startsWith('0 0 ') && n.props.width !== undefined);
    // the badger body, the eye and the eye's glint all share one view box + size
    const byBox = new Map<string, Set<string>>();
    for (const n of full) {
      const key = n.props.viewBox as string;
      byBox.set(key, (byBox.get(key) ?? new Set()).add(n.props.width + 'x' + n.props.height));
    }
    for (const [box, sizes] of byBox) expect({box, distinctSizes: sizes.size}).toEqual({box, distinctSizes: 1});
    act(() => tree.unmount());
  });

  it('calls onDone exactly once, after the full timeline', () => {
    const onDone = jest.fn();
    const tree = mount(onDone);
    act(() => jest.advanceTimersByTime(SPLASH_TOTAL - 300));
    expect(onDone).not.toHaveBeenCalled();
    act(() => jest.advanceTimersByTime(600));
    expect(onDone).toHaveBeenCalledTimes(1);
    act(() => jest.advanceTimersByTime(5000));
    expect(onDone).toHaveBeenCalledTimes(1);
    act(() => tree.unmount());
  });

  it('can be tapped away, and then does not fire a second time', () => {
    const onDone = jest.fn();
    const tree = mount(onDone);
    act(() => jest.advanceTimersByTime(500));
    const skip = tree.root.findAll(n => n.props?.accessibilityLabel === 'Skip intro')[0];
    act(() => skip.props.onPress());
    expect(onDone).toHaveBeenCalledTimes(1);
    act(() => jest.advanceTimersByTime(SPLASH_TOTAL + 1000));
    expect(onDone).toHaveBeenCalledTimes(1);
    act(() => tree.unmount());
  });

  it('with reduced motion, skips the shine and finishes quickly', () => {
    const onDone = jest.fn();
    const tree = mount(onDone, {reduceMotion: true});
    expect(JSON.stringify(tree.toJSON())).not.toContain('skewX');
    act(() => jest.advanceTimersByTime(1200));
    expect(onDone).toHaveBeenCalledTimes(1);
    act(() => tree.unmount());
  });

  it('uses the shine only when motion is allowed', () => {
    const tree = mount(() => {});
    expect(JSON.stringify(tree.toJSON())).toContain('skewX');
    act(() => tree.unmount());
  });

  it('is a sane length', () => {
    expect(SPLASH_TOTAL).toBe(SPLASH_TIMING.hold + SPLASH_TIMING.exit);
  });
});

describe('SplashGate', () => {
  beforeEach(() => {
    resetSplashForTests();
    jest.useFakeTimers();
  });
  afterEach(() => jest.useRealTimers());

  const hasSplash = (tree: renderer.ReactTestRenderer) =>
    tree.root.findAll(n => n.props?.accessibilityLabel === 'Super Badger').length > 0;

  const mountGate = async () => {
    let tree!: renderer.ReactTestRenderer;
    await act(async () => {
      tree = renderer.create(
        <ThemeProvider>
          <SplashGate>{React.createElement('View' as any, {testID: 'app'})}</SplashGate>
        </ThemeProvider>,
      );
    });
    return tree;
  };

  it('renders the app underneath, plays the splash once settings are loaded, then removes it', async () => {
    await settingsStore.load();
    const tree = await mountGate();
    expect(tree.root.findAll(n => n.props?.testID === 'app').length).toBeGreaterThan(0);
    expect(hasSplash(tree)).toBe(true);
    await act(async () => {
      jest.advanceTimersByTime(SPLASH_TOTAL + 500);
    });
    expect(hasSplash(tree)).toBe(false);
    // the app is still there
    expect(tree.root.findAll(n => n.props?.testID === 'app').length).toBeGreaterThan(0);
    await act(async () => tree.unmount());
  });

  it('does not replay after it has played (e.g. on a remount)', async () => {
    await settingsStore.load();
    const first = await mountGate();
    await act(async () => {
      jest.advanceTimersByTime(SPLASH_TOTAL + 500);
    });
    await act(async () => first.unmount());
    const second = await mountGate();
    expect(hasSplash(second)).toBe(false);
    await act(async () => second.unmount());
  });

  it('shows the splash in the saved theme, not the default one', async () => {
    await settingsStore.load();
    await settingsStore.save({...settingsStore.get(), themeName: 'ember'});
    const tree = await mountGate();
    expect(hasSplash(tree)).toBe(true);
    expect(bgOf(tree)).toBe(THEMES.ember.bg);
    await act(async () => tree.unmount());
    await settingsStore.save({...settingsStore.get(), themeName: 'light'});
  });
});

describe('SplashBoundary', () => {
  it('drops a splash that throws and reports it, instead of crashing the app', () => {
    const spy = jest.spyOn(console, 'error').mockImplementation(() => {});
    const onFail = jest.fn();
    const Bomb = () => {
      throw new Error('boom');
    };
    let tree!: renderer.ReactTestRenderer;
    act(() => {
      tree = renderer.create(
        <SplashBoundary onFail={onFail}>
          <Bomb />
        </SplashBoundary>,
      );
    });
    expect(onFail).toHaveBeenCalledTimes(1);
    expect(tree.toJSON()).toBeNull();
    spy.mockRestore();
  });

  it('passes a healthy splash straight through', () => {
    const onFail = jest.fn();
    let tree!: renderer.ReactTestRenderer;
    act(() => {
      tree = renderer.create(
        <SplashBoundary onFail={onFail}>{React.createElement('View' as any, {testID: 'ok'})}</SplashBoundary>,
      );
    });
    expect(onFail).not.toHaveBeenCalled();
    expect(tree.root.findAll(n => n.props?.testID === 'ok').length).toBeGreaterThan(0);
  });
});

void text;
