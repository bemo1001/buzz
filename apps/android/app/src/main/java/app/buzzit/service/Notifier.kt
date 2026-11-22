package app.buzzit.service

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.Person
import androidx.core.content.ContextCompat
import app.buzzit.MainActivity
import app.buzzit.R
import app.buzzit.data.api.Buzz
import app.buzzit.data.api.Message
import app.buzzit.data.api.User
import app.buzzit.data.db.ConversationEntity
import app.buzzit.data.repo.displayTitle
import app.buzzit.data.repo.members
import app.buzzit.data.repo.previewText

/** Message, friend-request and buzz-status notifications. */
class Notifier(private val context: Context) {

    private val nm = NotificationManagerCompat.from(context)

    private fun allowed() = android.os.Build.VERSION.SDK_INT < 33 ||
        ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

    private fun open(conversationId: String?): PendingIntent {
        val intent = Intent(context, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
            .putExtra(MainActivity.EXTRA_CONVERSATION, conversationId)
        return PendingIntent.getActivity(context, conversationId.hashCode(), intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    }

    @android.annotation.SuppressLint("MissingPermission")
    fun message(m: Message, c: ConversationEntity, me: String) {
        if (!allowed()) return
        val sender = c.members().firstOrNull { it.user.id == m.senderId }?.user
        val person = Person.Builder().setName(sender?.displayName ?: "Someone").setKey(m.senderId).build()
        val style = NotificationCompat.MessagingStyle(Person.Builder().setName("You").setKey(me).build())
            .setConversationTitle(if (c.type == "group") c.displayTitle(me) else null)
            .setGroupConversation(c.type == "group")
            .addMessage(m.previewText(), m.createdAt, person)
        val n = NotificationCompat.Builder(context, CH_MESSAGES)
            .setSmallIcon(R.drawable.ic_notification)
            .setStyle(style)
            .setContentIntent(open(c.id))
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .build()
        nm.notify(c.id.hashCode(), n)
    }

    fun cancelConversation(conversationId: String) = nm.cancel(conversationId.hashCode())

    @android.annotation.SuppressLint("MissingPermission")
    fun friendRequest(u: User) {
        if (!allowed()) return
        nm.notify(
            ("fr" + u.id).hashCode(),
            NotificationCompat.Builder(context, CH_SOCIAL)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle("${u.displayName} wants to be friends")
                .setContentText("@${u.username}")
                .setContentIntent(open(null))
                .setAutoCancel(true)
                .build(),
        )
    }

    @android.annotation.SuppressLint("MissingPermission")
    fun buzzAcknowledged(b: Buzz) {
        if (!allowed()) return
        nm.notify(
            b.id.hashCode(),
            NotificationCompat.Builder(context, CH_SOCIAL)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle("Your buzz was answered")
                .setContentText("They tapped “I'm OK”.")
                .setContentIntent(open(null))
                .setAutoCancel(true)
                .build(),
        )
    }

    fun cancelAll() = nm.cancelAll()

    companion object {
        const val CH_MESSAGES = "messages"
        const val CH_SOCIAL = "social"
        const val CH_CONNECTION = "connection"

        fun createChannels(ctx: Context) {
            val nm = ctx.getSystemService(NotificationManager::class.java)
            nm.createNotificationChannel(NotificationChannel(CH_MESSAGES, "Messages", NotificationManager.IMPORTANCE_HIGH))
            nm.createNotificationChannel(NotificationChannel(CH_SOCIAL, "Friends & buzz updates", NotificationManager.IMPORTANCE_DEFAULT))
            nm.createNotificationChannel(
                NotificationChannel(CH_CONNECTION, "Background connection", NotificationManager.IMPORTANCE_MIN).apply {
                    description = "Keeps Buzzit connected so messages and buzzes arrive instantly"
                    setShowBadge(false)
                },
            )
        }
    }
}
