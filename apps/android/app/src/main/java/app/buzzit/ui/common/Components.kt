package app.buzzit.ui.common

import android.text.format.DateFormat
import android.text.format.DateUtils
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import app.buzzit.AppContainer
import app.buzzit.BuzzitApp
import app.buzzit.data.api.ApiException
import coil.compose.AsyncImage
import java.util.Calendar
import java.util.Date
import kotlin.math.abs

@Composable
fun container(): AppContainer = (LocalContext.current.applicationContext as BuzzitApp).container

private val palette = listOf(
    Color(0xFFE57373), Color(0xFFF06292), Color(0xFFBA68C8), Color(0xFF7986CB), Color(0xFF4FC3F7),
    Color(0xFF4DB6AC), Color(0xFF81C784), Color(0xFFFFB74D), Color(0xFFA1887F), Color(0xFF90A4AE),
)

@Composable
fun Avatar(name: String, mediaId: String?, size: Dp = 48.dp, online: Boolean = false, modifier: Modifier = Modifier) {
    val c = container()
    Box(modifier.size(size)) {
        if (mediaId != null) {
            AsyncImage(
                model = c.api.mediaUrl(mediaId), contentDescription = name, contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize().clip(CircleShape),
            )
        } else {
            Box(
                Modifier.fillMaxSize().clip(CircleShape).background(palette[abs(name.hashCode()) % palette.size]),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    name.trim().split(Regex("\\s+")).take(2).mapNotNull { it.firstOrNull()?.uppercaseChar() }.joinToString(""),
                    color = Color.White, fontWeight = FontWeight.SemiBold, fontSize = (size.value * 0.38f).sp,
                )
            }
        }
        if (online) {
            Box(
                Modifier.align(Alignment.BottomEnd).size(size * 0.28f).clip(CircleShape)
                    .background(MaterialTheme.colorScheme.surface).padding(2.dp).clip(CircleShape).background(Color(0xFF43A047)),
            )
        }
    }
}

/** "14:05", "Yesterday", "Mon", or "12/03/26". */
@Composable
fun shortTime(millis: Long): String {
    if (millis <= 0) return ""
    val ctx = LocalContext.current
    val now = Calendar.getInstance()
    val then = Calendar.getInstance().apply { timeInMillis = millis }
    return when {
        DateUtils.isToday(millis) -> DateFormat.getTimeFormat(ctx).format(Date(millis))
        DateUtils.isToday(millis + DateUtils.DAY_IN_MILLIS) -> "Yesterday"
        now.timeInMillis - millis < 6 * DateUtils.DAY_IN_MILLIS -> DateFormat.format("EEE", then).toString()
        else -> DateFormat.getDateFormat(ctx).format(Date(millis))
    }
}

@Composable
fun clockTime(millis: Long): String = DateFormat.getTimeFormat(LocalContext.current).format(Date(millis))

fun lastSeenText(online: Boolean?, lastSeen: Long): String? = when {
    online == true -> "online"
    lastSeen > 0 -> "last seen " + DateUtils.getRelativeTimeSpanString(lastSeen, System.currentTimeMillis(), DateUtils.MINUTE_IN_MILLIS)
    else -> null
}

/** Human message for any error thrown by the API layer. */
fun Throwable.friendly(): String = (this as? ApiException)?.message ?: (message ?: "Something went wrong")
