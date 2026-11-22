package app.buzzit.buzz

import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings

/**
 * Permissions that decide how strong a Buzz can be on this device. Shown in
 * Settings → Buzz → "Make sure buzzes get through" with a live checklist.
 */
object BuzzPermissions {

    data class Report(
        val notifications: Boolean,
        val dndBypass: Boolean,     // Notification Policy Access
        val fullScreen: Boolean,    // USE_FULL_SCREEN_INTENT (Android 14+)
    ) {
        val emergencyReady get() = notifications && dndBypass && fullScreen
    }

    fun check(ctx: Context): Report {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        return Report(
            notifications = nm.areNotificationsEnabled(),
            dndBypass = nm.isNotificationPolicyAccessGranted,
            fullScreen = Build.VERSION.SDK_INT < 34 || nm.canUseFullScreenIntent(),
        )
    }

    fun dndSettingsIntent() = Intent(Settings.ACTION_NOTIFICATION_POLICY_ACCESS_SETTINGS)

    fun fullScreenSettingsIntent(ctx: Context) =
        if (Build.VERSION.SDK_INT >= 34) {
            Intent(Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, Uri.parse("package:${ctx.packageName}"))
        } else null
}
