package app.buzzit.ui.chat

import android.app.Application
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.NotificationsActive
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import app.buzzit.Routes
import app.buzzit.data.db.MessageEntity
import app.buzzit.data.repo.displayTitle
import app.buzzit.data.repo.members
import app.buzzit.data.repo.peer
import app.buzzit.ui.common.Avatar
import app.buzzit.ui.common.lastSeenText
import app.buzzit.ui.theme.BuzzAmber

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatScreen(conversationId: String, nav: NavHostController) {
    val ctx = LocalContext.current
    val vm: ChatViewModel = viewModel(key = conversationId, factory = ChatViewModel.Factory(ctx.applicationContext as Application, conversationId))
    val conv by vm.conversation.collectAsState()
    val messages by vm.messages.collectAsState()
    val typing by vm.typing.collectAsState()
    val presence by vm.presence.collectAsState()
    val error by vm.error.collectAsState()
    val buzzLevel by vm.buzzLevel.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    val listState = rememberLazyListState()

    var replyTo by remember { mutableStateOf<MessageEntity?>(null) }
    var editing by remember { mutableStateOf<MessageEntity?>(null) }
    var menuFor by remember { mutableStateOf<MessageEntity?>(null) }
    var showBuzz by remember { mutableStateOf(false) }
    var topMenu by remember { mutableStateOf(false) }
    var confirmLeave by remember { mutableStateOf(false) }

    DisposableEffect(conversationId) {
        vm.onOpen()
        onDispose { vm.onClose() }
    }
    LaunchedEffect(error) {
        error?.let { snackbar.showSnackbar(it); vm.clearError() }
    }
    LaunchedEffect(Unit) {
        vm.buzzUpdates.collect { b ->
            val text = when (b.status) {
                "delivered" -> "Buzz delivered"
                "rang" -> "Their phone is ringing…"
                "acked" -> "They answered: I'm OK ✅"
                else -> null
            }
            text?.let { snackbar.showSnackbar(it) }
        }
    }
    // Auto-scroll to the newest message when I send one or I'm already at the bottom.
    LaunchedEffect(messages.firstOrNull()?.id) {
        if (listState.firstVisibleItemIndex <= 2) listState.animateScrollToItem(0)
    }
    val nearTop by remember { derivedStateOf { listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0 } }
    LaunchedEffect(Unit) {
        snapshotFlow { nearTop }.collect { if (it >= messages.size - 10 && messages.size >= 30) vm.loadOlder() }
    }

    val me = vm.me
    val c = conv
    val title = c?.displayTitle(me) ?: ""
    val peer = c?.peer(me)
    val members = remember(c?.membersJson) { c?.members().orEmpty().associateBy { it.user.id } }
    val typers = typing[conversationId].orEmpty().filter { it.value > System.currentTimeMillis() && it.key != me }.keys
    val subtitle = when {
        typers.isNotEmpty() && c?.type == "group" -> typers.joinToString { members[it]?.user?.displayName ?: "Someone" } + " typing…"
        typers.isNotEmpty() -> "typing…"
        c?.type == "group" -> "${members.size} members"
        c?.type == "bot" -> "bot · not end-to-end encrypted"
        peer != null -> presence[peer.id]?.let { lastSeenText(it.online, it.lastSeenAt) } ?: lastSeenText(peer.online, peer.lastSeenAt)
        else -> null
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            TopAppBar(
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                title = {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier.clickable(enabled = c?.type == "group") { nav.navigate(Routes.groupInfo(conversationId)) },
                    ) {
                        Avatar(title, if (c?.type == "group") c.avatarId else peer?.avatarId, size = 36.dp)
                        Spacer(Modifier.width(10.dp))
                        Column {
                            Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            subtitle?.let { Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                        }
                    }
                },
                actions = {
                    if (c?.type == "direct") {
                        IconButton(onClick = { showBuzz = true }) { Icon(Icons.Filled.NotificationsActive, "Buzz", tint = BuzzAmber) }
                    }
                    IconButton(onClick = { topMenu = true }) { Icon(Icons.Filled.MoreVert, "More") }
                    DropdownMenu(topMenu, { topMenu = false }) {
                        val muted = (c?.mutedUntil ?: 0) > System.currentTimeMillis()
                        DropdownMenuItem(text = { Text(if (muted) "Unmute" else "Mute") }, onClick = { topMenu = false; vm.setMuted(!muted) })
                        if (c?.type == "group") {
                            DropdownMenuItem(text = { Text("Group info") }, onClick = { topMenu = false; nav.navigate(Routes.groupInfo(conversationId)) })
                            DropdownMenuItem(text = { Text("Leave group") }, onClick = { topMenu = false; confirmLeave = true })
                        }
                    }
                },
            )
        },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize().imePadding()) {
            LazyColumn(state = listState, reverseLayout = true, modifier = Modifier.weight(1f).fillMaxWidth()) {
                itemsIndexed(messages, key = { _, m -> m.id }) { i, m ->
                    val older = messages.getOrNull(i + 1)
                    val showName = c?.type == "group" && m.senderId != me && older?.senderId != m.senderId
                    MessageBubble(
                        m = m, mine = m.senderId == me,
                        senderName = if (showName) members[m.senderId]?.user?.displayName else null,
                        replied = m.replyTo?.let { rid -> messages.firstOrNull { it.id == rid } },
                        repliedName = { id -> if (id == me) "You" else members[id]?.user?.displayName ?: "" },
                        read = m.senderId == me && m.seq <= (c?.peerReadSeq ?: 0),
                        mediaUrl = vm::mediaUrl, token = vm.token(),
                        onLongPress = { menuFor = m },
                        onRetry = { vm.retry(m) },
                        dayHeader = older == null || !sameDay(older.createdAt, m.createdAt),
                    )
                }
            }
            Composer(
                replyTo = replyTo, replyName = replyTo?.let { if (it.senderId == me) "You" else members[it.senderId]?.user?.displayName },
                editing = editing,
                onCancel = { replyTo = null; editing = null },
                onTyping = vm::typing,
                onSend = { text ->
                    val e = editing
                    if (e != null) vm.edit(e, text) else vm.sendText(text, replyTo?.id)
                    replyTo = null; editing = null
                },
                onMedia = { kind, uri, mime, name -> vm.sendMedia(kind, uri, mime, name) },
            )
        }
    }

    menuFor?.let { m ->
        MessageActions(
            m = m, mine = m.senderId == me, canDeleteOthers = c?.myRole == "owner" || c?.myRole == "admin",
            onDismiss = { menuFor = null },
            onReact = { vm.react(m, it) },
            onReply = { replyTo = m; editing = null },
            onEdit = { editing = m; replyTo = null },
            onDelete = { vm.delete(m) },
            onCopy = { copy(ctx, m.text.orEmpty()) },
        )
    }

    if (showBuzz && peer != null) {
        BuzzSheet(
            name = peer.displayName, allowed = buzzLevel, onDismiss = { showBuzz = false },
            onSend = { level, note ->
                vm.buzz(peer.id, level, note) { if (it != null) showBuzz = false }
            },
        )
    }

    if (confirmLeave) {
        AlertDialog(
            onDismissRequest = { confirmLeave = false },
            title = { Text("Leave “$title”?") },
            text = { Text("You'll stop receiving messages from this group.") },
            confirmButton = { TextButton(onClick = { confirmLeave = false; vm.leave { nav.popBackStack() } }) { Text("Leave") } },
            dismissButton = { TextButton(onClick = { confirmLeave = false }) { Text("Cancel") } },
        )
    }
}

private fun sameDay(a: Long, b: Long): Boolean {
    val ca = java.util.Calendar.getInstance().apply { timeInMillis = a }
    val cb = java.util.Calendar.getInstance().apply { timeInMillis = b }
    return ca.get(java.util.Calendar.YEAR) == cb.get(java.util.Calendar.YEAR) && ca.get(java.util.Calendar.DAY_OF_YEAR) == cb.get(java.util.Calendar.DAY_OF_YEAR)
}

private fun copy(ctx: Context, text: String) {
    ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("message", text))
}

/** Reads display name and size of a content URI. */
fun Context.fileName(uri: Uri): String =
    contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
        if (it.moveToFirst()) it.getString(0) else null
    } ?: uri.lastPathSegment ?: "file"

@Composable
fun rememberPickers(onMedia: (kind: String, uri: String, mime: String, name: String) -> Unit): Pair<() -> Unit, () -> Unit> {
    val ctx = LocalContext.current
    fun handle(uri: Uri?, forceKind: String?) {
        uri ?: return
        val mime = ctx.contentResolver.getType(uri) ?: "application/octet-stream"
        val kind = forceKind ?: when {
            mime.startsWith("image/") -> "image"
            mime.startsWith("video/") -> "video"
            mime.startsWith("audio/") -> "voice"
            else -> "file"
        }
        onMedia(kind, uri.toString(), mime, ctx.fileName(uri))
    }
    val photo = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { handle(it, null) }
    val file = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        uri?.let { runCatching { ctx.contentResolver.takePersistableUriPermission(it, android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION) } }
        handle(uri, "file")
    }
    return Pair(
        { photo.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo)) },
        { file.launch(arrayOf("*/*")) },
    )
}
