import path from 'path';

import {defineConfig} from 'vite';
import react from '@vitejs/plugin-react';

// Aliases "react-native" -> "react-native-web" so the exact same src/App.tsx
// used by Metro (native/Android) renders in a browser here, with real Vite
// dev-server HMR — see app/README.md. The server URL/auth token are runtime
// settings (src/settings.ts, editable from the in-app Settings screen), not
// build-time config, so there's nothing to inject here.
export default defineConfig(({mode}) => ({
  plugins: [react({include: /\.(jsx|js|tsx|ts)$/})],
  resolve: {
    alias: {
      // More specific "react-native/..." aliases must precede the generic
      // "react-native" -> "react-native-web" one below — Vite matches alias
      // keys in order, and the generic prefix would otherwise win first.
      //
      // See codegenNativeComponentWebStub.ts — react-native-svg's Fabric-only
      // native-component files pull this in even on the web build, but it
      // has no react-native-web equivalent to alias to.
      'react-native/Libraries/Utilities/codegenNativeComponent': path.resolve(__dirname, 'src/stubs/codegenNativeComponentWebStub.ts'),
      // See reactNativeWebShim.ts — react-native-svg imports TurboModuleRegistry
      // directly from "react-native" (a named export react-native-web doesn't
      // have), via files that reach it through relative imports Vite's alias
      // can't intercept on their own, so the shim patches it in at this level
      // instead of aliasing straight to "react-native-web".
      'react-native': path.resolve(__dirname, 'src/stubs/reactNativeWebShim.ts'),
      // @notifee/react-native is native-only (background notifications,
      // Android-only, always behind a dynamic import()). Its real package
      // requires a bare RN internal path that doesn't exist under
      // react-native-web — harmless in the production build (Rollup tree-
      // shakes the whole Android-only branch away since it's never
      // reachable), but Vite's dev-server dependency prescan crawls it
      // eagerly regardless of reachability and crashes outright. Aliasing
      // to a stub sidesteps that scan entirely — see the stub's own comment
      // for the exact failure this fixes.
      '@notifee/react-native': path.resolve(__dirname, 'src/stubs/notifeeWebStub.ts'),
      // See assetsRegistryWebStub.ts — react-native-svg's asset-URI resolver
      // pulls in this Flow-syntax-only native package even on its web build.
      '@react-native/assets-registry/registry': path.resolve(__dirname, 'src/stubs/assetsRegistryWebStub.ts'),
    },
    extensions: [
      '.web.tsx',
      '.web.ts',
      '.web.jsx',
      '.web.js',
      '.tsx',
      '.ts',
      '.jsx',
      '.js',
    ],
  },
  define: {
    // react-native-web checks this at runtime.
    'process.env.NODE_ENV': JSON.stringify(mode === 'production' ? 'production' : 'development'),
  },
}));
