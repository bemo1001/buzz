package app.buzzit.buzz

import app.buzzit.BuzzitApp
import app.buzzit.service.DeviceRegistration
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import kotlinx.coroutines.launch

/**
 * Optional FCM wake-ups (only when Firebase is configured). The server sends
 * high-priority *data* messages without content:
 *   type=buzz  -> ring immediately (buzz_id, level, sender_name, note)
 *   type=sync  -> fetch new events over our own connection
 */
class BuzzMessagingService : FirebaseMessagingService() {

    override fun onMessageReceived(message: RemoteMessage) {
        val data = message.data
        val c = (application as BuzzitApp).container
        when (data["type"]) {
            "buzz" -> {
                val id = data["buzz_id"] ?: return
                BuzzAlertService.start(this, id, data["level"], data["sender_name"], null, data["note"], snoozed = false)
            }
            "sync" -> c.scope.launch {
                c.realtime.start()
                runCatching { c.sync.catchUp() }
            }
        }
    }

    override fun onNewToken(token: String) = DeviceRegistration.updatePushToken(this, token)
}
