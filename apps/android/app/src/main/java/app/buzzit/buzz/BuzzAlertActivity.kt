package app.buzzit.buzz

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Bundle
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.NotificationsActive
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.scale
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import app.buzzit.MainActivity

/** Full-screen alert shown over the lock screen for Buzz and Emergency. */
class BuzzAlertActivity : ComponentActivity() {

    private val closed = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) = finish()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (android.os.Build.VERSION.SDK_INT >= 27) {
            setShowWhenLocked(true)
            setTurnScreenOn(true)
        } else {
            @Suppress("DEPRECATION")
            window.addFlags(WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED or WindowManager.LayoutParams.FLAG_TURN_SCREEN_ON)
        }
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        ContextCompat.registerReceiver(this, closed, IntentFilter(BuzzAlertService.ACTION_CLOSED), ContextCompat.RECEIVER_NOT_EXPORTED)

        val id = intent.getStringExtra(BuzzAlertService.EXTRA_ID) ?: return finish()
        val level = BuzzLevel.from(intent.getStringExtra(BuzzAlertService.EXTRA_LEVEL))
        val sender = intent.getStringExtra(BuzzAlertService.EXTRA_SENDER_NAME) ?: "A friend"
        val senderId = intent.getStringExtra(BuzzAlertService.EXTRA_SENDER_ID)
        val note = intent.getStringExtra(BuzzAlertService.EXTRA_NOTE)

        setContent {
            BuzzScreen(
                level = level, sender = sender, note = note,
                onOk = {
                    BuzzAlertService.dismiss(this, id)
                    BuzzStatusReporter.report(this, id, "acked")
                    finish()
                },
                onReply = {
                    BuzzAlertService.dismiss(this, id)
                    BuzzStatusReporter.report(this, id, "acked")
                    startActivity(
                        Intent(this, MainActivity::class.java)
                            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
                            .putExtra(MainActivity.EXTRA_OPEN_USER, senderId),
                    )
                    finish()
                },
                onSnooze = {
                    BuzzAlertService.dismiss(this, id)
                    BuzzAlertService.snooze(this, id, level.wire, sender, senderId, note)
                    finish()
                },
            )
        }
    }

    override fun onDestroy() {
        unregisterReceiver(closed)
        super.onDestroy()
    }

    companion object {
        fun intent(ctx: Context, id: String, level: BuzzLevel, sender: String, senderId: String?, note: String?) =
            Intent(ctx, BuzzAlertActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NO_USER_ACTION)
                .putExtra(BuzzAlertService.EXTRA_ID, id)
                .putExtra(BuzzAlertService.EXTRA_LEVEL, level.wire)
                .putExtra(BuzzAlertService.EXTRA_SENDER_NAME, sender)
                .putExtra(BuzzAlertService.EXTRA_SENDER_ID, senderId)
                .putExtra(BuzzAlertService.EXTRA_NOTE, note)
    }
}

@Composable
private fun BuzzScreen(level: BuzzLevel, sender: String, note: String?, onOk: () -> Unit, onReply: () -> Unit, onSnooze: () -> Unit) {
    val emergency = level == BuzzLevel.EMERGENCY
    val bg = if (emergency) Color(0xFFB71C1C) else Color(0xFF1E1A10)
    val accent = if (emergency) Color.White else Color(0xFFFFC107)
    val pulse by rememberInfiniteTransition(label = "pulse").animateFloat(
        initialValue = 0.9f, targetValue = 1.15f,
        animationSpec = infiniteRepeatable(tween(450), RepeatMode.Reverse), label = "scale",
    )
    Box(Modifier.fillMaxSize().background(bg).padding(24.dp), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Box(Modifier.size(140.dp).scale(pulse).background(accent.copy(alpha = 0.18f), CircleShape), contentAlignment = Alignment.Center) {
                Icon(Icons.Filled.NotificationsActive, contentDescription = null, tint = accent, modifier = Modifier.size(72.dp))
            }
            Text(if (emergency) "EMERGENCY BUZZ" else "BUZZ!", color = accent, fontSize = 18.sp, fontWeight = FontWeight.Bold, letterSpacing = 4.sp)
            Text(sender, color = Color.White, fontSize = 34.sp, fontWeight = FontWeight.Bold, textAlign = TextAlign.Center)
            if (!note.isNullOrBlank()) {
                Text("“$note”", color = Color.White.copy(alpha = 0.9f), fontSize = 20.sp, textAlign = TextAlign.Center)
            }
            Spacer(Modifier.height(24.dp))
            Button(
                onClick = onOk, modifier = Modifier.fillMaxWidth().height(60.dp),
                colors = ButtonDefaults.buttonColors(containerColor = accent, contentColor = bg),
            ) { Text("I'm OK", fontSize = 20.sp, fontWeight = FontWeight.Bold) }
            OutlinedButton(onClick = onReply, modifier = Modifier.fillMaxWidth().height(52.dp)) {
                Text("Reply to $sender", color = Color.White)
            }
            OutlinedButton(onClick = onSnooze, modifier = Modifier.fillMaxWidth().height(52.dp)) {
                Text("Snooze 5 min", color = Color.White)
            }
        }
    }
}
