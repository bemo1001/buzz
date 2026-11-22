package app.buzzit.ui.chat

import android.media.AudioAttributes
import android.media.MediaPlayer
import android.net.Uri
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.AttachFile
import androidx.compose.material.icons.filled.Done
import androidx.compose.material.icons.filled.DoneAll
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Schedule
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import app.buzzit.data.db.MessageEntity
import app.buzzit.data.db.MessageState
import app.buzzit.data.repo.media
import app.buzzit.data.repo.reactions
import app.buzzit.data.repo.system
import app.buzzit.data.repo.systemText
import app.buzzit.ui.common.clockTime
import app.buzzit.ui.common.shortTime
import coil.compose.AsyncImage

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun MessageBubble(
    m: MessageEntity,
    mine: Boolean,
    senderName: String?,
    replied: MessageEntity?,
    repliedName: (String) -> String,
    read: Boolean,
    mediaUrl: (String) -> String,
    token: String,
    onLongPress: () -> Unit,
    onRetry: () -> Unit,
    dayHeader: Boolean,
) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 2.dp)) {
        if (dayHeader) {
            Text(
                shortTime(m.createdAt).let { if (it.contains(':')) "Today" else it },
                style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp),
            )
        }
        if (m.kind == "system") {
            Text(
                systemText(m.system()), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
            )
            return
        }
        val bg = if (mine) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceVariant
        val fg = if (mine) MaterialTheme.colorScheme.onPrimaryContainer else MaterialTheme.colorScheme.onSurfaceVariant
        Row(Modifier.fillMaxWidth(), horizontalArrangement = if (mine) Arrangement.End else Arrangement.Start) {
            Surface(
                color = bg, contentColor = fg,
                shape = RoundedCornerShape(18.dp, 18.dp, if (mine) 4.dp else 18.dp, if (mine) 18.dp else 4.dp),
                modifier = Modifier.widthIn(max = 300.dp).clip(RoundedCornerShape(18.dp))
                    .combinedClickable(onClick = { if (m.state == MessageState.FAILED) onRetry() }, onLongClick = onLongPress),
            ) {
                Column(Modifier.padding(horizontal = 12.dp, vertical = 8.dp)) {
                    senderName?.let { Text(it, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary, fontWeight = FontWeight.SemiBold) }
                    replied?.let { r ->
                        Column(
                            Modifier.padding(bottom = 6.dp).clip(RoundedCornerShape(8.dp))
                                .background(MaterialTheme.colorScheme.surface.copy(alpha = 0.5f)).padding(8.dp),
                        ) {
                            Text(repliedName(r.senderId), style = MaterialTheme.typography.labelSmall, fontWeight = FontWeight.Bold)
                            Text(r.text ?: "Attachment", maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall)
                        }
                    }
                    when {
                        m.deleted -> Text("This message was deleted", fontStyle = FontStyle.Italic)
                        else -> {
                            MediaContent(m, mediaUrl, token)
                            if (!m.text.isNullOrEmpty()) Text(m.text, style = MaterialTheme.typography.bodyLarge)
                        }
                    }
                    Row(Modifier.align(Alignment.End), verticalAlignment = Alignment.CenterVertically) {
                        if (m.editedAt > 0 && !m.deleted) Text("edited · ", style = MaterialTheme.typography.labelSmall)
                        Text(clockTime(m.createdAt), style = MaterialTheme.typography.labelSmall)
                        if (mine) {
                            Spacer(Modifier.width(4.dp))
                            val (icon, desc) = when {
                                m.state == MessageState.PENDING -> Icons.Filled.Schedule to "Sending"
                                m.state == MessageState.FAILED -> Icons.Filled.ErrorOutline to "Failed, tap to retry"
                                read -> Icons.Filled.DoneAll to "Read"
                                else -> Icons.Filled.Done to "Sent"
                            }
                            Icon(
                                icon, desc, Modifier.size(14.dp),
                                tint = when {
                                    m.state == MessageState.FAILED -> MaterialTheme.colorScheme.error
                                    read -> MaterialTheme.colorScheme.primary
                                    else -> fg
                                },
                            )
                        }
                    }
                }
            }
        }
        val reactions = m.reactions()
        if (reactions.isNotEmpty()) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp), horizontalArrangement = if (mine) Arrangement.End else Arrangement.Start) {
                Surface(shape = RoundedCornerShape(12.dp), tonalElevation = 3.dp, modifier = Modifier.clickable(onClick = onLongPress)) {
                    val grouped = reactions.groupBy { it.emoji }
                    Text(
                        grouped.entries.joinToString(" ") { (e, l) -> if (l.size > 1) "$e${l.size}" else e },
                        modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp),
                    )
                }
            }
        }
    }
}

@Composable
private fun MediaContent(m: MessageEntity, mediaUrl: (String) -> String, token: String) {
    val ctx = LocalContext.current
    val media = m.media()
    val local = m.localUri
    when (m.kind) {
        "image" -> {
            val model: Any? = media?.let { mediaUrl(it.id) } ?: local
            AsyncImage(
                model = model, contentDescription = "Photo", contentScale = ContentScale.Crop,
                modifier = Modifier.widthIn(max = 260.dp).heightIn(min = 120.dp, max = 320.dp).clip(RoundedCornerShape(12.dp))
                    .clickable(enabled = media != null) { media?.let { openMedia(ctx, mediaUrl(it.id), it.mime) } },
            )
        }
        "voice" -> if (media != null) VoicePlayer(mediaUrl(media.id), token) else Text("🎤 Sending voice message…")
        "video" -> Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.clickable(enabled = media != null) { media?.let { openMedia(ctx, mediaUrl(it.id), it.mime) } },
        ) {
            Icon(Icons.Filled.Videocam, null)
            Spacer(Modifier.width(6.dp))
            Text(media?.filename?.ifEmpty { "Video" } ?: m.localName ?: "Video")
        }
        "file" -> Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.clickable(enabled = media != null) { media?.let { openMedia(ctx, mediaUrl(it.id), it.mime) } },
        ) {
            Icon(Icons.Filled.AttachFile, null)
            Spacer(Modifier.width(6.dp))
            Column {
                Text(media?.filename?.ifEmpty { "File" } ?: m.localName ?: "File", maxLines = 1, overflow = TextOverflow.Ellipsis)
                media?.let { Text(humanSize(it.size), style = MaterialTheme.typography.labelSmall) }
            }
        }
    }
}

fun humanSize(bytes: Long): String = when {
    bytes >= 1 shl 20 -> "%.1f MB".format(bytes / 1048576.0)
    bytes >= 1 shl 10 -> "%.0f KB".format(bytes / 1024.0)
    else -> "$bytes B"
}

/** Media is private, so files open through our app's downloader. */
private fun openMedia(ctx: android.content.Context, url: String, mime: String) {
    MediaDownloads.open(ctx, url, mime)
}

@Composable
private fun VoicePlayer(url: String, token: String) {
    val ctx = LocalContext.current
    var playing by remember { mutableStateOf(false) }
    val player = remember { mutableStateOf<MediaPlayer?>(null) }
    DisposableEffect(url) { onDispose { player.value?.release(); player.value = null } }
    Row(verticalAlignment = Alignment.CenterVertically) {
        IconButton(onClick = {
            val p = player.value
            if (playing && p != null) {
                p.pause(); playing = false
            } else if (p != null) {
                p.start(); playing = true
            } else {
                val mp = MediaPlayer().apply {
                    setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_MEDIA).setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build())
                    setDataSource(ctx, Uri.parse(url), mapOf("Authorization" to "Bearer $token"))
                    setOnPreparedListener { it.start(); playing = true }
                    setOnCompletionListener { playing = false }
                    prepareAsync()
                }
                player.value = mp
            }
        }) { Icon(if (playing) Icons.Filled.Pause else Icons.Filled.PlayArrow, if (playing) "Pause" else "Play") }
        Box(Modifier.width(120.dp).padding(end = 8.dp)) { Text("Voice message", style = MaterialTheme.typography.bodyMedium) }
    }
}
