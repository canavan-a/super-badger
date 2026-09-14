package com.superbadgerapp

import android.app.DownloadManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import androidx.core.content.FileProvider
import com.facebook.react.bridge.Arguments
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.WritableMap
import com.facebook.react.modules.core.DeviceEventManagerModule
import java.io.File

// In-app updater for the sideloaded APK (mirrors ../horus-33/mobile's
// AppUpdaterModule): reports the installed version, downloads a release APK
// via the system DownloadManager, and hands it to the package installer.
// Android always shows the installer's confirmation UI — there is no silent
// replace for a non-device-owner app.
class AppUpdaterModule(private val reactCtx: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactCtx) {

  override fun getName() = "AppUpdater"

  private val dm: DownloadManager
    get() = reactCtx.getSystemService(Context.DOWNLOAD_SERVICE) as DownloadManager

  @ReactMethod
  fun getVersionInfo(promise: Promise) {
    try {
      val pm = reactCtx.packageManager
      val pkg = reactCtx.packageName
      val info = pm.getPackageInfo(pkg, 0)
      val code =
          if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) info.longVersionCode
          else @Suppress("DEPRECATION") info.versionCode.toLong()
      val map = Arguments.createMap()
      map.putString("versionName", info.versionName ?: "")
      map.putDouble("versionCode", code.toDouble())
      map.putString("packageName", pkg)
      promise.resolve(map)
    } catch (e: Exception) {
      promise.reject("version_info_failed", e)
    }
  }

  @ReactMethod
  fun canInstallPackages(promise: Promise) {
    val ok =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O)
            reactCtx.packageManager.canRequestPackageInstalls()
        else true
    promise.resolve(ok)
  }

  @ReactMethod
  fun openInstallPermissionSettings(promise: Promise) {
    try {
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
        val intent =
            Intent(
                    Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                    Uri.parse("package:" + reactCtx.packageName))
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        reactCtx.startActivity(intent)
      }
      promise.resolve(null)
    } catch (e: Exception) {
      promise.reject("open_settings_failed", e)
    }
  }

  @ReactMethod
  fun downloadApk(url: String, versionLabel: String, promise: Promise) {
    try {
      val fileName = "superbadger-$versionLabel.apk"
      // Clear any stale copy so the installer never picks up a half-written file.
      File(reactCtx.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS), fileName).delete()

      val req =
          DownloadManager.Request(Uri.parse(url))
              .setTitle("super-badger $versionLabel")
              .setDescription("Downloading update")
              .setMimeType("application/vnd.android.package-archive")
              .setNotificationVisibility(
                  DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
              .setDestinationInExternalFilesDir(
                  reactCtx, Environment.DIRECTORY_DOWNLOADS, fileName)

      val id = dm.enqueue(req)
      trackProgress(id)

      val receiver =
          object : BroadcastReceiver() {
            override fun onReceive(c: Context, i: Intent) {
              val done = i.getLongExtra(DownloadManager.EXTRA_DOWNLOAD_ID, -1)
              if (done != id) return
              try {
                reactCtx.unregisterReceiver(this)
              } catch (_: Exception) {}

              val q = DownloadManager.Query().setFilterById(id)
              dm.query(q).use { cur ->
                if (cur == null || !cur.moveToFirst()) {
                  promise.reject("download_failed", "download row missing")
                  return
                }
                val status =
                    cur.getInt(cur.getColumnIndexOrThrow(DownloadManager.COLUMN_STATUS))
                if (status == DownloadManager.STATUS_SUCCESSFUL) {
                  val local =
                      File(
                              reactCtx.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS),
                              fileName)
                          .absolutePath
                  promise.resolve(local)
                } else {
                  val reason =
                      cur.getInt(cur.getColumnIndexOrThrow(DownloadManager.COLUMN_REASON))
                  promise.reject("download_failed", "status=$status reason=$reason")
                }
              }
            }
          }
      val filter = IntentFilter(DownloadManager.ACTION_DOWNLOAD_COMPLETE)
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        reactCtx.registerReceiver(receiver, filter, Context.RECEIVER_EXPORTED)
      } else {
        @Suppress("UnspecifiedRegisterReceiverFlag") reactCtx.registerReceiver(receiver, filter)
      }
    } catch (e: Exception) {
      promise.reject("download_failed", e)
    }
  }

  // Poll the download row until it settles, emitting AppUpdaterProgress events so
  // JS can render a percentage.
  private fun trackProgress(id: Long) {
    val handler = Handler(Looper.getMainLooper())
    val poll =
        object : Runnable {
          override fun run() {
            val q = DownloadManager.Query().setFilterById(id)
            var running = false
            dm.query(q)?.use { cur ->
              if (cur.moveToFirst()) {
                val status =
                    cur.getInt(cur.getColumnIndexOrThrow(DownloadManager.COLUMN_STATUS))
                val bytes =
                    cur.getLong(
                        cur.getColumnIndexOrThrow(
                            DownloadManager.COLUMN_BYTES_DOWNLOADED_SO_FAR))
                val total =
                    cur.getLong(
                        cur.getColumnIndexOrThrow(
                            DownloadManager.COLUMN_TOTAL_SIZE_BYTES))
                emitProgress(bytes, total)
                running =
                    status == DownloadManager.STATUS_RUNNING ||
                        status == DownloadManager.STATUS_PENDING ||
                        status == DownloadManager.STATUS_PAUSED
              }
            }
            if (running) handler.postDelayed(this, 400)
          }
        }
    handler.post(poll)
  }

  private fun emitProgress(bytes: Long, total: Long) {
    val map: WritableMap = Arguments.createMap()
    map.putDouble("bytesDownloaded", bytes.toDouble())
    map.putDouble("bytesTotal", total.toDouble())
    reactCtx
        .getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
        .emit("AppUpdaterProgress", map)
  }

  @ReactMethod
  fun installApk(path: String, promise: Promise) {
    try {
      val file = File(path)
      val uri =
          FileProvider.getUriForFile(reactCtx, reactCtx.packageName + ".updateprovider", file)
      val intent =
          Intent(Intent.ACTION_VIEW)
              .setDataAndType(uri, "application/vnd.android.package-archive")
              .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
              .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
      reactCtx.startActivity(intent)
      promise.resolve(null)
    } catch (e: Exception) {
      promise.reject("install_failed", e)
    }
  }

  // Needed for NativeEventEmitter on the JS side (no-op listener bookkeeping).
  @ReactMethod fun addListener(eventName: String) {}

  @ReactMethod fun removeListeners(count: Int) {}
}
