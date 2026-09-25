This is a new [**React Native**](https://reactnative.dev) project, bootstrapped using [`@react-native-community/cli`](https://github.com/react-native-community/cli).

# Getting Started

>**Note**: Make sure you have completed the [React Native - Environment Setup](https://reactnative.dev/docs/environment-setup) instructions till "Creating a new application" step, before proceeding.

## Step 1: Start the Metro Server

First, you will need to start **Metro**, the JavaScript _bundler_ that ships _with_ React Native.

To start Metro, run the following command from the _root_ of your React Native project:

```bash
# using npm
npm start

# OR using Yarn
yarn start
```

## Step 2: Start your Application

Let Metro Bundler run in its _own_ terminal. Open a _new_ terminal from the _root_ of your React Native project. Run the following command to start your _Android_ or _iOS_ app:

### For Android

```bash
# using npm
npm run android

# OR using Yarn
yarn android
```

### For iOS

```bash
# using npm
npm run ios

# OR using Yarn
yarn ios
```

If everything is set up _correctly_, you should see your new app running in your _Android Emulator_ or _iOS Simulator_ shortly provided you have set up your emulator/simulator correctly.

This is one way to run your app — you can also run it directly from within Android Studio and Xcode respectively.

## Step 3: Modifying your App

Now that you have successfully run the app, let's modify it.

1. Open `App.tsx` in your text editor of choice and edit some lines.
2. For **Android**: Press the <kbd>R</kbd> key twice or select **"Reload"** from the **Developer Menu** (<kbd>Ctrl</kbd> + <kbd>M</kbd> (on Window and Linux) or <kbd>Cmd ⌘</kbd> + <kbd>M</kbd> (on macOS)) to see your changes!

   For **iOS**: Hit <kbd>Cmd ⌘</kbd> + <kbd>R</kbd> in your iOS Simulator to reload the app and see your changes!

## Congratulations! :tada:

You've successfully run and modified your React Native App. :partying_face:

### Now what?

- If you want to add this new React Native code to an existing application, check out the [Integration guide](https://reactnative.dev/docs/integration-with-existing-apps).
- If you're curious to learn more about React Native, check out the [Introduction to React Native](https://reactnative.dev/docs/getting-started).

# Launch splash and themed icon

The app opens on the same badger and wordmark as the terminal client's title
screen (`src/logo/`), recolored for the current theme: the badger settles in,
the letters rise in one by one, then a slanted shine sweeps across and catches
the badger's eye. Tap to skip; it honors the system "reduce motion" setting, and
plays once per launch. To watch it on its own, run `npm run web` and open
`/splash-preview.html?theme=ember` (any of `light dark slate sepia ember`).

**The launcher icon is the blackletter S from the wordmark, one per theme.**
Android has no API to swap an app's icon, so the manifest declares one launcher
entry (an `<activity-alias>`) per theme and `AppIconModule.kt` enables the one
matching the theme. It switches only as the app goes to the background — some
launchers flicker, drop a pinned shortcut or restart the app when the entry they
point at is disabled — and can be turned off in Settings ("Match app icon to
theme"). Adaptive icons (API 26+, with an Android 13 themed-icon layer) and
legacy PNGs are both generated.

**Both come from the terminal art, and are generated, not drawn.** Edit
`tui/art/superbadger.ans`, then from `tui/`:

    go run ./tools/enhance   # rebuild the high-res title art
    go run ./tools/appart    # -> app/src/logo/logoData.ts, iconPalette.json, launcher icons

(`-preview <dir>` also renders PNGs of the logo and icons for every theme.)
Theme colors are read from `src/theme.tsx`; add a theme there and re-run.

# Troubleshooting

If you can't get this to work, see the [Troubleshooting](https://reactnative.dev/docs/troubleshooting) page.

# Learn More

To learn more about React Native, take a look at the following resources:

- [React Native Website](https://reactnative.dev) - learn more about React Native.
- [Getting Started](https://reactnative.dev/docs/environment-setup) - an **overview** of React Native and how setup your environment.
- [Learn the Basics](https://reactnative.dev/docs/getting-started) - a **guided tour** of the React Native **basics**.
- [Blog](https://reactnative.dev/blog) - read the latest official React Native **Blog** posts.
- [`@facebook/react-native`](https://github.com/facebook/react-native) - the Open Source; GitHub **repository** for React Native.
