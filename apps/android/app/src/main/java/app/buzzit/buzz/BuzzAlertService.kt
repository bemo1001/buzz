package app.buzzit.buzz

import android.app.AlarmManager
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.media.AudioAttributes
import android.media.AudioManager
import android.media.MediaPlayer
import android.media.RingtoneManager
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.SystemClock
import android.os.VibrationEffect
import android.os.Vibrator
import android.os.VibratorManager
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import app.buzzit.R
import app.buzzit.data.api.Buzz
import app.buzzit.data.api.User

/**
 * Plays a Buzz even when the phone is on silent / vibrate / Do Not Disturb.
 *
 *  - Sound plays on the ALARM stream (USAGE_ALARM), which ringer mode does not mute.
 *  - The Emergency channel bypasses DND when the user granted Notification Policy
 *    Access (see [BuzzPermissions]).
 *  - A full-screen intent shows [BuzzAlertActivity] over the lock screen.
 *
 * Nudge = short haptic + heads-up. Buzz = loud for up to 15 s. Emergency = loops
 * until answered (auto-stops after 2 minutes).
 */
class BuzzAlertService : Service() {

    private var player: MediaPlayer? = null
    private var currentId: String? = null
    private val handler = Handler(Looper.getMainLooper())

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_DISMISS -> {
                val id = intent.getStringExtra(EXTRA_ID)
                if (id == null || id == currentId) finish()
                return START_NOT_STICKY
            }
        }
        val id = intent?.getStringExtra(EXTRA_ID) ?: return START_NOT_STICKY.also { stopSelf() }
        val level = BuzzLevel.from(intent.getStringExtra(EXTRA_LEVEL))
        val sender = intent.getStringExtra(EXTRA_SENDER_NAME) ?: "A friend"
        val senderId = intent.getStringExtra(EXTRA_SENDER_ID)
        val note = intent.getStringExtra(EXTRA_NOTE)
        val snoozed = intent.getBooleanExtra(EXTRA_SNOOZED, false)

        if (!snoozed && !BuzzDedup.firstTime(this, id)) {
            if (currentId == null) stopSelf()
            return START_NOT_STICKY
        }
        stopSound()
        currentId = id
        val notification = buildNotification(id, level, sender, senderId, note)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(NOTIF_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK)
        } else {
            startForeground(NOTIF_ID, notification)
        }
        if (!snoozed) BuzzStatusReporter.report(this, id, "delivered")
        if (level != BuzzLevel.NUDGE) playAlarm(level)
        vibrate(level)
        if (!snoozed) BuzzStatusReporter.report(this, id, "rang")

        handler.removeCallbacksAndMessages(null)
        // Stop ringing after a while but leave the notification so it can still be answered.
        val ringFor = when (level) { BuzzLevel.NUDGE -> 3_000L; BuzzLevel.BUZZ -> 15_000L; BuzzLevel.EMERGENCY -> 120_000L }
        handler.postDelayed({ finish(keepNotification = true) }, ringFor)
        return START_NOT_STICKY
    }

    private fun buildNotification(id: String, level: BuzzLevel, sender: String, senderId: String?, note: String?): Notification {
        val open = PendingIntent.getActivity(
            this, id.hashCode(), BuzzAlertActivity.intent(this, id, level, sender, senderId, note),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val ack = PendingIntent.getBroadcast(
            this, id.hashCode() + 1, Intent(this, BuzzActionReceiver::class.java).setAction(BuzzActionReceiver.ACK).putExtra(EXTRA_ID, id),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return NotificationCompat.Builder(this, level.channelId)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(
                when (level) {
                    BuzzLevel.EMERGENCY -> "EMERGENCY buzz from $sender"
                    BuzzLevel.BUZZ -> "$sender is buzzing you"
                    BuzzLevel.NUDGE -> "$sender nudged you"
                },
            )
            .setContentText(note?.takeIf { it.isNotBlank() } ?: "Tap to respond")
            .setCategory(if (level == BuzzLevel.NUDGE) NotificationCompat.CATEGORY_MESSAGE else NotificationCompat.CATEGORY_ALARM)
            .setPriority(NotificationCompat.PRIORITY_MAX)
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            .setOngoing(level == BuzzLevel.EMERGENCY)
            .setContentIntent(open)
            .addAction(0, "I'm OK", ack)
            .apply { if (level != BuzzLevel.NUDGE) setFullScreenIntent(open, true) }
            .build()
    }

    private fun playAlarm(level: BuzzLevel) {
        val am = getSystemService(AudioManager::class.java)
        // Respect the user's alarm volume, but never let an Emergency be silent.
        if (level == BuzzLevel.EMERGENCY && am.getStreamVolume(AudioManager.STREAM_ALARM) == 0) {
            am.setStreamVolume(AudioManager.STREAM_ALARM, am.getStreamMaxVolume(AudioManager.STREAM_ALARM) / 2, 0)
        }
        val uri = RingtoneManager.getDefaultUri(RingtoneManager.TYPE_ALARM)
            ?: RingtoneManager.getDefaultUri(RingtoneManager.TYPE_RINGTONE) ?: return
        player = runCatching {
            MediaPlayer().apply {
                setAudioAttributes(
                    AudioAttributes.Builder()
                        .setUsage(AudioAttributes.USAGE_ALARM)
                        .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                        .build(),
                )
                setDataSource(this@BuzzAlertService, uri)
                isLooping = true
                prepare()
                start()
            }
        }.getOrNull()
    }

    private fun vibrator(): Vibrator = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
        getSystemService(VibratorManager::class.java).defaultVibrator
    } else {
        @Suppress("DEPRECATION") getSystemService(Vibrator::class.java)
    }

    private fun vibrate(level: BuzzLevel) {
        val pattern = when (level) {
            BuzzLevel.NUDGE -> longArrayOf(0, 120, 80, 120)
            BuzzLevel.BUZZ -> longArrayOf(0, 400, 150, 400, 150, 800, 600)
            BuzzLevel.EMERGENCY -> longArrayOf(0, 800, 200, 800, 200, 800, 600)
        }
        val repeat = if (level == BuzzLevel.NUDGE) -1 else 0
        val attrs = AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_ALARM).build()
        @Suppress("DEPRECATION")
        vibrator().vibrate(VibrationEffect.createWaveform(pattern, repeat), attrs)
    }

    private fun stopSound() {
        player?.runCatching { stop(); release() }
        player = null
        vibrator().cancel()
    }

    private fun finish(keepNotification: Boolean = false) {
        handler.removeCallbacksAndMessages(null)
        stopSound()
        currentId = null
        stopForeground(if (keepNotification) STOP_FOREGROUND_DETACH else STOP_FOREGROUND_REMOVE)
        stopSelf()
        sendBroadcast(Intent(ACTION_CLOSED).setPackage(packageName))
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        stopSound()
        super.onDestroy()
    }

    companion object {
        const val EXTRA_ID = "buzz_id"
        const val EXTRA_LEVEL = "level"
        const val EXTRA_SENDER_NAME = "sender_name"
        const val EXTRA_SENDER_ID = "sender_id"
        const val EXTRA_NOTE = "note"
        const val EXTRA_SNOOZED = "snoozed"
        const val ACTION_DISMISS = "app.buzzit.buzz.DISMISS"
        /** Broadcast when the alert stops (closes the full-screen activity). */
        const val ACTION_CLOSED = "app.buzzit.buzz.CLOSED"
        private const val NOTIF_ID = 0xB022

        fun start(ctx: Context, buzz: Buzz, sender: User) = start(ctx, buzz.id, buzz.level, sender.displayName, sender.id, buzz.note, false)

        fun start(ctx: Context, id: String, level: String?, senderName: String?, senderId: String?, note: String?, snoozed: Boolean) {
            val intent = Intent(ctx, BuzzAlertService::class.java)
                .putExtra(EXTRA_ID, id).putExtra(EXTRA_LEVEL, level).putExtra(EXTRA_SENDER_NAME, senderName)
                .putExtra(EXTRA_SENDER_ID, senderId).putExtra(EXTRA_NOTE, note).putExtra(EXTRA_SNOOZED, snoozed)
            runCatching { ContextCompat.startForegroundService(ctx, intent) }
        }

        /** Stop ringing (id = null stops whatever is ringing). */
        fun dismiss(ctx: Context, id: String?) {
            ctx.getSystemService(NotificationManager::class.java).cancel(NOTIF_ID)
            runCatching { ctx.startService(Intent(ctx, BuzzAlertService::class.java).setAction(ACTION_DISMISS).putExtra(EXTRA_ID, id)) }
        }

        /** Ring again in 5 minutes. */
        fun snooze(ctx: Context, id: String, level: String, senderName: String?, senderId: String?, note: String?) {
            val intent = Intent(ctx, BuzzActionReceiver::class.java).setAction(BuzzActionReceiver.RESNOOZE)
                .putExtra(EXTRA_ID, id).putExtra(EXTRA_LEVEL, level).putExtra(EXTRA_SENDER_NAME, senderName)
                .putExtra(EXTRA_SENDER_ID, senderId).putExtra(EXTRA_NOTE, note)
            val pi = PendingIntent.getBroadcast(ctx, id.hashCode() + 2, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
            ctx.getSystemService(AlarmManager::class.java)
                .setAndAllowWhileIdle(AlarmManager.ELAPSED_REALTIME_WAKEUP, SystemClock.elapsedRealtime() + 5 * 60_000, pi)
        }

        fun ensureChannels(ctx: Context) {
            val nm = ctx.getSystemService(NotificationManager::class.java)
            BuzzLevel.entries.forEach { level ->
                val ch = NotificationChannel(level.channelId, level.channelName, NotificationManager.IMPORTANCE_HIGH).apply {
                    setSound(null, null) // we play audio ourselves on the alarm stream
                    enableVibration(false)
                    lockscreenVisibility = Notification.VISIBILITY_PUBLIC
                    if (level != BuzzLevel.NUDGE) setBypassDnd(true)
                }
                nm.createNotificationChannel(ch)
            }
        }
    }
}

/** Handles the notification's "I'm OK" action and snooze alarms. */
class BuzzActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val id = intent.getStringExtra(BuzzAlertService.EXTRA_ID) ?: return
        when (intent.action) {
            ACK -> {
                BuzzAlertService.dismiss(context, id)
                BuzzStatusReporter.report(context, id, "acked")
            }
            RESNOOZE -> BuzzAlertService.start(
                context, id, intent.getStringExtra(BuzzAlertService.EXTRA_LEVEL), intent.getStringExtra(BuzzAlertService.EXTRA_SENDER_NAME),
                intent.getStringExtra(BuzzAlertService.EXTRA_SENDER_ID), intent.getStringExtra(BuzzAlertService.EXTRA_NOTE), snoozed = true,
            )
        }
    }

    companion object {
        const val ACK = "app.buzzit.buzz.ACK"
        const val RESNOOZE = "app.buzzit.buzz.RESNOOZE"
    }
}

/** Remembers recent buzz IDs so the same buzz (via socket and push) rings once. */
object BuzzDedup {
    @Synchronized
    fun firstTime(ctx: Context, id: String): Boolean {
        val prefs = ctx.getSharedPreferences("buzz_seen", Context.MODE_PRIVATE)
        val now = System.currentTimeMillis()
        if (prefs.contains(id)) return false
        val edit = prefs.edit().putLong(id, now)
        prefs.all.forEach { (k, v) -> if (v is Long && now - v > 86_400_000) edit.remove(k) }
        edit.apply()
        return true
    }
}

enum class BuzzLevel(val wire: String, val channelId: String, val channelName: String) {
    NUDGE("nudge", "buzz_nudge", "Nudges"),
    BUZZ("buzz", "buzz_buzz", "Buzzes"),
    EMERGENCY("emergency", "buzz_emergency", "Emergency buzzes");

    companion object {
        fun from(s: String?) = entries.firstOrNull { it.wire == s } ?: NUDGE
    }
}
