package app.buzzit.buzz

import android.content.Intent
import androidx.core.content.ContextCompat
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage

/**
 * Receives FCM high-priority *data* messages (never notification messages,
 * which Android would render itself with the default, silenceable channel).
 *
 * Payload (set by delivery-svc):
 *   type=buzz, buzz_id, level, sender_name, note?
 */
class BuzzMessagingService : FirebaseMessagingService() {

    override fun onMessageReceived(message: RemoteMessage) {
        val data = message.data
        if (data["type"] != "buzz") return
        val id = data["buzz_id"] ?: return
        BuzzStatusReporter.report(this, id, "delivered")
        val intent = Intent(this, BuzzAlertService::class.java)
            .putExtra(BuzzAlertService.EXTRA_ID, id)
            .putExtra(BuzzAlertService.EXTRA_LEVEL, data["level"])
            .putExtra(BuzzAlertService.EXTRA_SENDER_NAME, data["sender_name"])
            .putExtra(BuzzAlertService.EXTRA_NOTE, data["note"])
        // High-priority FCM grants a short window to start a foreground service.
        ContextCompat.startForegroundService(this, intent)
    }

    override fun onNewToken(token: String) {
        DeviceRegistration.updatePushToken(this, token)
    }
}
