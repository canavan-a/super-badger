package com.superbadgerapp

import android.content.ComponentName
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Canvas
import androidx.core.content.ContextCompat
import com.facebook.react.bridge.Promise
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import java.io.File
import java.io.FileOutputStream

/**
 * Switches the launcher icon between the per-theme versions.
 *
 * Android has no API to change an app's icon directly. The manifest instead
 * declares one launcher entry per theme (an <activity-alias> named IconLight,
 * IconDark, ..., each with its own icon) and exactly one of them is enabled at
 * any time; enabling another swaps the icon the launcher shows. The JS side
 * (src/appIcon.ts) only calls this while the app is going to the background,
 * because some launchers flicker, drop a pinned shortcut, or restart the app
 * when the entry they point at is disabled.
 */
class AppIconModule(private val reactContext: ReactApplicationContext) :
    ReactContextBaseJavaModule(reactContext) {

  override fun getName(): String = "AppIcon"

  private val themes = listOf("light", "dark", "slate", "sepia", "ember")

  private fun alias(theme: String) =
      ComponentName(
          reactContext.packageName,
          "com.superbadgerapp.Icon" + theme.replaceFirstChar { it.uppercase() })

  /** The manifest enables IconLight by default; the others start disabled. */
  private fun isEnabled(theme: String): Boolean =
      when (reactContext.packageManager.getComponentEnabledSetting(alias(theme))) {
        PackageManager.COMPONENT_ENABLED_STATE_ENABLED -> true
        PackageManager.COMPONENT_ENABLED_STATE_DEFAULT -> theme == DEFAULT
        else -> false
      }

  /** Resolves with the theme whose icon is currently showing. */
  @ReactMethod
  fun getIcon(promise: Promise) {
    promise.resolve(themes.firstOrNull { isEnabled(it) } ?: DEFAULT)
  }

  /** Makes [name]'s icon the launcher icon. Resolves true on success. */
  @ReactMethod
  fun setIcon(name: String, promise: Promise) {
    if (name !in themes) {
      promise.reject("E_BAD_ICON", "Unknown icon: $name")
      return
    }
    try {
      val pm = reactContext.packageManager
      // Enable the new entry before disabling the old ones, so there is never
      // a moment with no launcher entry at all. DONT_KILL_APP keeps the app
      // running while its own launcher entry changes.
      pm.setComponentEnabledSetting(
          alias(name), PackageManager.COMPONENT_ENABLED_STATE_ENABLED, PackageManager.DONT_KILL_APP)
      for (t in themes) {
        if (t != name) {
          pm.setComponentEnabledSetting(
              alias(t), PackageManager.COMPONENT_ENABLED_STATE_DISABLED, PackageManager.DONT_KILL_APP)
        }
      }
      promise.resolve(true)
    } catch (e: Exception) {
      promise.reject("E_SET_ICON", e)
    }
  }

  /**
   * Resolves with a file:// URI of [name]'s launcher icon rendered to a PNG,
   * for a notification's large icon. Without one, a notification can only show
   * the app's static <application android:icon>, which is always the light
   * one whatever alias is enabled. Notifee loads large icons through Fresco,
   * which can't decode the adaptive-icon XML mipmaps, hence the PNG. Rendered
   * once per theme and cached.
   */
  @ReactMethod
  fun notificationIconUri(name: String, promise: Promise) {
    if (name !in themes) {
      promise.reject("E_BAD_ICON", "Unknown icon: $name")
      return
    }
    try {
      val file = File(reactContext.cacheDir, "notif_icon_${name}_v$ICON_VERSION.png")
      if (!file.exists()) {
        val res =
            reactContext.resources.getIdentifier(
                "ic_launcher_$name", "mipmap", reactContext.packageName)
        val drawable =
            ContextCompat.getDrawable(reactContext, res)
                ?: throw IllegalStateException("No launcher icon for $name")
        val bitmap = Bitmap.createBitmap(ICON_PX, ICON_PX, Bitmap.Config.ARGB_8888)
        drawable.setBounds(0, 0, ICON_PX, ICON_PX)
        drawable.draw(Canvas(bitmap))
        // Write then rename, so a half-written file is never picked up as cached.
        val tmp = File(file.path + ".tmp")
        FileOutputStream(tmp).use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        tmp.renameTo(file)
      }
      promise.resolve("file://" + file.absolutePath)
    } catch (e: Exception) {
      promise.reject("E_ICON_URI", e)
    }
  }

  companion object {
    private const val DEFAULT = "light"
    private const val ICON_PX = 192
    // Bump when the generated launcher art changes, so PNGs cached by an
    // older build aren't reused.
    private const val ICON_VERSION = 1
  }
}
