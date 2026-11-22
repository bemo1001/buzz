package app.buzzit.ui.chat

import android.Manifest
import android.content.pm.PackageManager
import android.media.MediaRecorder
import android.net.Uri
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Reply
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.AttachFile
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.Image
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material3.AssistChip
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import app.buzzit.data.db.MessageEntity
import app.buzzit.data.db.MessageState
import kotlinx.coroutines.delay
import java.io.File

@Composable
fun Composer(
    replyTo: MessageEntity?,
    replyName: String?,
    editing: MessageEntity?,
    onCancel: () -> Unit,
    onTyping: () -> Unit,
    onSend: (String) -> Unit,
    onMedia: (kind: String, uri: String, mime: String, name: String) -> Unit,
) {
    val ctx = LocalContext.current
    var text by remember { mutableStateOf("") }
    var attachMenu by remember { mutableStateOf(false) }
    val (pickPhoto, pickFile) = rememberPickers(onMedia)
    val recorder = remember { VoiceRecorder(ctx.cacheDir) }
    var recording by remember { mutableStateOf(false) }
    var recStart by remember { mutableLongStateOf(0L) }
    var elapsed by remember { mutableLongStateOf(0L) }
    val askMic = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (ok) { recording = recorder.start(ctx); recStart = System.currentTimeMillis() }
    }

    LaunchedEffect(editing?.id) { if (editing != null) text = editing.text.orEmpty() }
    LaunchedEffect(recording) {
        while (recording) { elapsed = System.currentTimeMillis() - recStart; delay(200) }
    }
    DisposableEffect(Unit) { onDispose { recorder.cancel() } }

    Surface(tonalElevation = 3.dp) {
        Column(Modifier.navigationBarsPadding()) {
            if (replyTo != null || editing != null) {
                Row(Modifier.fillMaxWidth().padding(start = 16.dp, top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                    Icon(if (editing != null) Icons.Filled.Edit else Icons.AutoMirrored.Filled.Reply, null, tint = MaterialTheme.colorScheme.primary)
                    Column(Modifier.weight(1f).padding(horizontal = 8.dp)) {
                        Text(if (editing != null) "Editing" else "Replying to ${replyName.orEmpty()}", style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.Bold)
                        Text((editing ?: replyTo)?.text ?: "Attachment", maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall)
                    }
                    IconButton(onClick = { onCancel(); if (editing != null) text = "" }) { Icon(Icons.Filled.Close, "Cancel") }
                }
            }
            Row(Modifier.fillMaxWidth().padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
                if (recording) {
                    IconButton(onClick = { recorder.cancel(); recording = false }) { Icon(Icons.Filled.Delete, "Discard recording") }
                    Text("● Recording  ${elapsed / 1000 / 60}:${"%02d".format(elapsed / 1000 % 60)}", color = MaterialTheme.colorScheme.error, modifier = Modifier.weight(1f))
                    FilledIconButton(onClick = {
                        recording = false
                        recorder.stop()?.let { f -> onMedia("voice", Uri.fromFile(f).toString(), "audio/mp4", f.name) }
                    }) { Icon(Icons.Filled.Stop, "Send voice message") }
                    return@Row
                }
                if (editing == null) {
                    IconButton(onClick = { attachMenu = true }) { Icon(Icons.Filled.AttachFile, "Attach") }
                    DropdownMenu(attachMenu, { attachMenu = false }) {
                        DropdownMenuItem(text = { Text("Photo or video") }, leadingIcon = { Icon(Icons.Filled.Image, null) }, onClick = { attachMenu = false; pickPhoto() })
                        DropdownMenuItem(text = { Text("File") }, leadingIcon = { Icon(Icons.Filled.AttachFile, null) }, onClick = { attachMenu = false; pickFile() })
                    }
                }
                OutlinedTextField(
                    value = text, onValueChange = { text = it; if (it.isNotEmpty()) onTyping() },
                    placeholder = { Text("Message") }, maxLines = 5, shape = RoundedCornerShape(24.dp),
                    modifier = Modifier.weight(1f),
                )
                if (text.isBlank() && editing == null) {
                    IconButton(onClick = {
                        if (ContextCompat.checkSelfPermission(ctx, Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
                            recording = recorder.start(ctx); recStart = System.currentTimeMillis()
                        } else askMic.launch(Manifest.permission.RECORD_AUDIO)
                    }) { Icon(Icons.Filled.Mic, "Record voice message") }
                } else {
                    FilledIconButton(onClick = { onSend(text); text = "" }, enabled = text.isNotBlank()) {
                        Icon(Icons.AutoMirrored.Filled.Send, "Send")
                    }
                }
            }
        }
    }
}

/** Records AAC voice notes into the cache directory. */
class VoiceRecorder(private val dir: File) {
    private var rec: MediaRecorder? = null
    private var file: File? = null

    fun start(ctx: android.content.Context): Boolean = runCatching {
        val f = File(dir, "voice_${System.currentTimeMillis()}.m4a")
        rec = (if (Build.VERSION.SDK_INT >= 31) MediaRecorder(ctx) else @Suppress("DEPRECATION") MediaRecorder()).apply {
            setAudioSource(MediaRecorder.AudioSource.MIC)
            setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
            setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
            setAudioEncodingBitRate(64_000)
            setAudioSamplingRate(44_100)
            setOutputFile(f.absolutePath)
            prepare()
            start()
        }
        file = f
        true
    }.getOrElse { cancel(); false }

    fun stop(): File? {
        val r = rec ?: return null
        rec = null
        val ok = runCatching { r.stop() }.isSuccess
        r.release()
        return file.takeIf { ok && (it?.length() ?: 0) > 0 }
    }

    fun cancel() {
        rec?.runCatching { stop() }
        rec?.release()
        rec = null
        file?.delete()
        file = null
    }
}

private val quickReactions = listOf("👍", "❤️", "😂", "😮", "😢", "🙏")

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun MessageActions(
    m: MessageEntity,
    mine: Boolean,
    canDeleteOthers: Boolean,
    onDismiss: () -> Unit,
    onReact: (String?) -> Unit,
    onReply: () -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
    onCopy: () -> Unit,
) {
    val sent = m.state == MessageState.SENT && !m.deleted
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(bottom = 24.dp)) {
            if (sent) {
                FlowRow(Modifier.fillMaxWidth().padding(horizontal = 16.dp), horizontalArrangement = Arrangement.SpaceEvenly) {
                    quickReactions.forEach { e ->
                        TextButton(onClick = { onReact(e); onDismiss() }) { Text(e, style = MaterialTheme.typography.headlineSmall) }
                    }
                }
                AssistChip(onClick = { onReact(null); onDismiss() }, label = { Text("Remove my reaction") }, modifier = Modifier.padding(horizontal = 16.dp))
                ListItem(headlineContent = { Text("Reply") }, leadingContent = { Icon(Icons.AutoMirrored.Filled.Reply, null) },
                    modifier = Modifier.clickable { onReply(); onDismiss() })
            }
            if (!m.text.isNullOrEmpty() && !m.deleted) {
                ListItem(headlineContent = { Text("Copy text") }, leadingContent = { Icon(Icons.Filled.ContentCopy, null) },
                    modifier = Modifier.clickable { onCopy(); onDismiss() })
            }
            if (sent && mine && m.kind == "text" && System.currentTimeMillis() - m.createdAt < 15 * 60_000) {
                ListItem(headlineContent = { Text("Edit") }, leadingContent = { Icon(Icons.Filled.Edit, null) },
                    modifier = Modifier.clickable { onEdit(); onDismiss() })
            }
            if (!m.deleted && (mine || canDeleteOthers)) {
                ListItem(
                    headlineContent = { Text(if (m.state == MessageState.SENT) "Delete for everyone" else "Discard", color = MaterialTheme.colorScheme.error) },
                    leadingContent = { Icon(Icons.Filled.Delete, null, tint = MaterialTheme.colorScheme.error) },
                    modifier = Modifier.clickable { onDelete(); onDismiss() },
                )
            }
        }
    }
}
