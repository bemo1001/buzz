package app.buzzit.buzz

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.media.AudioAttributes
import android.media.AudioManager
import android.media.MediaPlayer
import android.media.RingtoneManager
import android.os.Build
import android.os.IBinder
import android.os.VibrationEffect
import android.os.Vibrator
import android.os.VibratorManager
import androidx.core.app.NotificationCompat

/**
 * Plays a Buzz even when the phone is on silent / vibrate / DND.
 *
 * How it works:
 *  - Audio is played on the ALARM stream (USAGE_ALARM). Android's ringer mode
 *    (silent/vibrate) does not mute the alarm stream.
 *  - The notification channel for Emergency sets setBypassDnd(true). This only
 *    takes effect if the user granted Notification Policy Access
 *    (see [BuzzPermissions]); we ask during onboarding of Buzz Passes.
 *  - A full-screen intent shows [BuzzAlertActivity] over the lock screen.
 *    Android 14+ requires USE_FULL_SCREEN_INTENT to be granted; if not, we
 *    degrade to a heads-up notification while still playing alarm audio.
 *
 * Started from [BuzzMessagingService] when a high-priority FCM data message
 * with type=buzz arrives.
 */
class BuzzAlertService : Service() {

    private var player: MediaPlayer? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopAlert()
            stopSelf()
            return START_NOT_STICKY
        }
        val buzzId = intent?.getStringExtra(EXTRA_ID) ?: return START_NOT_STICKY
        val level = BuzzLevel.from(intent.getStringExtra(EXTRA_LEVEL))
        val sender = intent.getStringExtra(EXTRA_SENDER_NAME) ?: "A friend"
        val note = intent.getStringExtra(EXTRA_NOTE)

        ensureChannels(this)
        val notification = buildNotification(buzzId, level, sender, note)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(NOTIF_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK)
        } else {
            startForeground(NOTIF_ID, notification)
        }

        if (level != BuzzLevel.NUDGE) playAlarm(level)
        vibrate(level)
        BuzzStatusReporter.report(this, buzzId, "rang")
        return START_NOT_STICKY
    }

    private fun buildNotification(id: String, level: BuzzLevel, sender: String, note: String?) =
        NotificationCompat.Builder(this, level.channelId)
            .setSmallIcon(android.R.drawable.ic_lock_idle_alarm)
            .setContentTitle(if (level == BuzzLevel.EMERGENCY) "EMERGENCY buzz from $sender" else "$sender buzzed you")
            .setContentText(note ?: "Tap to respond")
            .setCategory(if (level == BuzzLevel.NUDGE) NotificationCompat.CATEGORY_MESSAGE else NotificationCompat.CATEGORY_ALARM)
            .setPriority(NotificationCompat.PRIORITY_MAX)
            .setOngoing(level == BuzzLevel.EMERGENCY)
            .apply {
                val open = PendingIntent.getActivity(
                    this@BuzzAlertService, id.hashCode(),
                    BuzzAlertActivity.intent(this@BuzzAlertService, id, level, sender, note),
                    PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
                )
                setContentIntent(open)
                if (level != BuzzLevel.NUDGE) setFullScreenIntent(open, true)
            }
            .build()

    private fun playAlarm(level: BuzzLevel) {
        val am = getSystemService(AudioManager::class.java)
        // Respect the user's alarm volume, but never play an Emergency at zero.
        if (level == BuzzLevel.EMERGENCY && am.getStreamVolume(AudioManager.STREAM_ALARM) == 0) {
            am.setStreamVolume(AudioManager.STREAM_ALARM, am.getStreamMaxVolume(AudioManager.STREAM_ALARM) / 2, 0)
        }
        player = MediaPlayer().apply {
            setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_ALARM)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                    .build()
            )
            setDataSource(this@BuzzAlertService, RingtoneManager.getDefaultUri(RingtoneManager.TYPE_ALARM))
            isLooping = level == BuzzLevel.EMERGENCY
            prepare()
            start()
        }
        if (level == BuzzLevel.BUZZ) {
            // A Buzz rings for at most 15 seconds.
            android.os.Handler(mainLooper).postDelayed({ stopAlert() }, 15_000)
        }
    }

    private fun vibrate(level: BuzzLevel) {
        val vibrator = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            getSystemService(VibratorManager::class.java).defaultVibrator
        } else {
            @Suppress("DEPRECATION") getSystemService(Vibrator::class.java)
        }
        val pattern = when (level) {
            BuzzLevel.NUDGE -> longArrayOf(0, 120, 80, 120)
            BuzzLevel.BUZZ -> longArrayOf(0, 400, 150, 400, 150, 800)
            BuzzLevel.EMERGENCY -> longArrayOf(0, 800, 200, 800, 200, 800, 600)
        }
        val repeat = if (level == BuzzLevel.EMERGENCY) 0 else -1
        val attrs = AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_ALARM).build()
        @Suppress("DEPRECATION")
        vibrator.vibrate(VibrationEffect.createWaveform(pattern, repeat), attrs)
    }

    private fun stopAlert() {
        player?.runCatching { stop(); release() }
        player = null
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            getSystemService(VibratorManager::class.java).cancel()
        } else {
            @Suppress("DEPRECATION") getSystemService(Vibrator::class.java).cancel()
        }
    }

    override fun onDestroy() {
        stopAlert()
        super.onDestroy()
    }

    companion object {
        const val EXTRA_ID = "buzz_id"
        const val EXTRA_LEVEL = "level"
        const val EXTRA_SENDER_NAME = "sender_name"
        const val EXTRA_NOTE = "note"
        const val ACTION_STOP = "app.buzzit.buzz.STOP"
        private const val NOTIF_ID = 0xB022

        fun ensureChannels(ctx: Context) {
            val nm = ctx.getSystemService(NotificationManager::class.java)
            BuzzLevel.entries.forEach { level ->
                val ch = NotificationChannel(level.channelId, level.channelName, NotificationManager.IMPORTANCE_HIGH).apply {
                    setSound(null, null) // we play audio ourselves on the alarm stream
                    enableVibration(false)
                    lockscreenVisibility = android.app.Notification.VISIBILITY_PUBLIC
                    if (level == BuzzLevel.EMERGENCY) setBypassDnd(true)
                }
                nm.createNotificationChannel(ch)
            }
        }

        fun stop(ctx: Context) = ctx.startService(Intent(ctx, BuzzAlertService::class.java).setAction(ACTION_STOP))
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
