package app.buzzit.service

import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import app.buzzit.BuzzitApp
import app.buzzit.MainActivity
import app.buzzit.R

/**
 * Keeps the realtime connection alive while the app is in the background, so
 * Buzzit works without Firebase/Google push. Users can turn it off in
 * Settings (then delivery relies on FCM, if configured).
 */
class ConnectionService : Service() {

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val app = application as BuzzitApp
        if (intent?.action == ACTION_STOP || app.container.session.token == null) {
            stopSelf()
            return START_NOT_STICKY
        }
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val n = NotificationCompat.Builder(this, Notifier.CH_CONNECTION)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("Buzzit is connected")
            .setContentText("Ready for messages and buzzes")
            .setContentIntent(open)
            .setOngoing(true)
            .setPriority(NotificationCompat.PRIORITY_MIN)
            .build()
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIF_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_REMOTE_MESSAGING)
        } else {
            startForeground(NOTIF_ID, n)
        }
        app.container.realtime.start()
        return START_STICKY
    }

    companion object {
        private const val NOTIF_ID = 0xC0
        private const val ACTION_STOP = "app.buzzit.connection.STOP"

        fun start(ctx: Context) {
            runCatching { ContextCompat.startForegroundService(ctx, Intent(ctx, ConnectionService::class.java)) }
        }

        fun stop(ctx: Context) {
            ctx.stopService(Intent(ctx, ConnectionService::class.java))
        }
    }
}
