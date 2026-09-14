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
      'react-native': 'react-native-web',
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
