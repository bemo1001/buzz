package app.buzzit.service

import android.content.Context
import android.util.Log
import app.buzzit.BuildConfig
import app.buzzit.BuzzitApp
import app.buzzit.data.api.jsonOf
import com.google.firebase.FirebaseApp
import com.google.firebase.FirebaseOptions
import com.google.firebase.messaging.FirebaseMessaging
import kotlinx.coroutines.launch

/** Optional Firebase push. Configure with gradle properties buzzit.fcm.*. */
object DeviceRegistration {

    private val configured get() = BuildConfig.FCM_APP_ID.isNotEmpty()

    fun initFirebaseIfConfigured(ctx: Context) {
        if (!configured || FirebaseApp.getApps(ctx).isNotEmpty()) return
        FirebaseApp.initializeApp(
            ctx,
            FirebaseOptions.Builder()
                .setProjectId(BuildConfig.FCM_PROJECT_ID)
                .setApplicationId(BuildConfig.FCM_APP_ID)
                .setApiKey(BuildConfig.FCM_API_KEY)
                .setGcmSenderId(BuildConfig.FCM_SENDER_ID)
                .build(),
        )
    }

    /** Fetch the FCM token and register it with the server. */
    fun syncPushToken(ctx: Context) {
        if (!configured) return
        runCatching {
            FirebaseMessaging.getInstance().isAutoInitEnabled = true
            FirebaseMessaging.getInstance().token.addOnSuccessListener { updatePushToken(ctx, it) }
        }.onFailure { Log.w("Push", "FCM unavailable", it) }
    }

    fun updatePushToken(ctx: Context, token: String) {
        val c = (ctx.applicationContext as BuzzitApp).container
        c.session.pushToken = token
        if (c.session.token == null) return
        c.scope.launch {
            runCatching { c.api.send("PUT", "/v1/devices/current/push-token", jsonOf("token" to token)) }
        }
    }
}
