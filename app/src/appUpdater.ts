// In-app update flow for the sideloaded Android APK — mirrors
// ../horus-33/mobile/src/lib/appUpdater.ts. There's no app store distribution
// (this is a sideloaded build, matching horus-33's own approach and
// super-badger's release.sh precedent), so "check for updates" means "look
// at this repo's GitHub Releases" and "install" means downloading the APK
// and handing it to Android's package installer directly — see
// android/.../AppUpdaterModule.kt for the native half of this.
import {NativeEventEmitter, NativeModules} from 'react-native';

interface VersionInfo {
  versionName: string;
  versionCode: number;
  packageName: string;
}

interface AppUpdaterNative {
  getVersionInfo(): Promise<VersionInfo>;
  canInstallPackages(): Promise<boolean>;
  openInstallPermissionSettings(): Promise<void>;
  downloadApk(url: string, versionLabel: string): Promise<string>;
  installApk(path: string): Promise<void>;
}

const Native = NativeModules.AppUpdater as AppUpdaterNative | undefined;

function native(): AppUpdaterNative {
  if (!Native) throw new Error('AppUpdater native module unavailable (rebuild the app)');
  return Native;
}

export const getVersionInfo = () => native().getVersionInfo();

export interface DownloadProgress {
  bytesDownloaded: number;
  bytesTotal: number;
}

export function onDownloadProgress(cb: (p: DownloadProgress) => void): () => void {
  if (!Native) return () => {};
  const emitter = new NativeEventEmitter(NativeModules.AppUpdater);
  const sub = emitter.addListener('AppUpdaterProgress', cb);
  return () => sub.remove();
}

// --- GitHub Releases ---

const REPO = 'canavan-a/super-badger';
const TAG_PREFIX = 'app-v';
const APK_NAME = /^superbadger.*\.apk$/;

export interface Release {
  tag: string;
  version: string;
  apkUrl: string;
  notes: string;
  publishedAt: string;
}

interface GhAsset {
  name: string;
  browser_download_url: string;
}
interface GhRelease {
  tag_name: string;
  body: string | null;
  published_at: string;
  draft: boolean;
  prerelease: boolean;
  assets: GhAsset[];
}

export async function fetchReleases(): Promise<Release[]> {
  const res = await fetch(`https://api.github.com/repos/${REPO}/releases?per_page=30`, {
    headers: {Accept: 'application/vnd.github+json'},
  });
  const text = await res.text();
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      msg = JSON.parse(text)?.message ?? msg;
    } catch {
      // keep the status line
    }
    throw new Error(msg);
  }
  const raw = JSON.parse(text) as GhRelease[];
  return raw
    .filter(r => !r.draft && r.tag_name.startsWith(TAG_PREFIX))
    .map((r): Release | null => {
      const apk = r.assets.find(a => APK_NAME.test(a.name)) ?? r.assets[0];
      if (!apk) return null;
      return {
        tag: r.tag_name,
        version: r.tag_name.slice(TAG_PREFIX.length),
        apkUrl: apk.browser_download_url,
        notes: r.body ?? '',
        publishedAt: r.published_at,
      };
    })
    .filter((r): r is Release => r !== null)
    .sort((a, b) => b.publishedAt.localeCompare(a.publishedAt));
}

// Returns > 0 if a is newer than b, < 0 if older, 0 if equal. Non-numeric
// segments (e.g. "1.2.0-rc1") compare lexically as a tie-breaker.
export function compareVersions(a: string, b: string): number {
  const pa = a.split(/[.-]/);
  const pb = b.split(/[.-]/);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const na = Number(pa[i]);
    const nb = Number(pb[i]);
    if (!Number.isNaN(na) && !Number.isNaN(nb)) {
      if (na !== nb) return na - nb;
    } else {
      const sa = pa[i] ?? '';
      const sb = pb[i] ?? '';
      if (sa !== sb) return sa < sb ? -1 : 1;
    }
  }
  return 0;
}

export class DowngradeBlockedError extends Error {}

// Orchestrates the update: permission gate -> download -> hand to installer.
// Throws DowngradeBlockedError when the installer rejects a lower versionCode.
export async function runUpdate(release: Release): Promise<void> {
  const n = native();
  if (!(await n.canInstallPackages())) {
    await n.openInstallPermissionSettings();
    throw new Error('Grant "install unknown apps" permission, then try again.');
  }
  const path = await n.downloadApk(release.apkUrl, release.version);
  try {
    await n.installApk(path);
  } catch (e) {
    const msg = String(e);
    if (/downgrade/i.test(msg) || /INSTALL_FAILED_VERSION_DOWNGRADE/.test(msg)) {
      throw new DowngradeBlockedError();
    }
    throw e;
  }
}
