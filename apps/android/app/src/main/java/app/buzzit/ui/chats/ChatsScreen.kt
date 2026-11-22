package app.buzzit.ui.chats

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.Group
import androidx.compose.material.icons.filled.NotificationsOff
import androidx.compose.material.icons.filled.SmartToy
import androidx.compose.material3.Badge
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import app.buzzit.Routes
import app.buzzit.data.api.Friend
import app.buzzit.data.api.RealtimeClient
import app.buzzit.data.db.ConversationEntity
import app.buzzit.data.repo.displayTitle
import app.buzzit.data.repo.peer
import app.buzzit.openChat
import app.buzzit.ui.common.Avatar
import app.buzzit.ui.common.container
import app.buzzit.ui.common.friendly
import app.buzzit.ui.common.shortTime
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatsTab(nav: NavHostController, modifier: Modifier) {
    val c = container()
    val scope = rememberCoroutineScope()
    val conversations by c.chats.conversations().collectAsState(initial = emptyList())
    val typing by c.sync.typing.collectAsState()
    val presence by c.sync.presence.collectAsState()
    val conn by c.realtime.state.collectAsState()
    var refreshing by remember { mutableStateOf(false) }
    val me = c.session.userId

    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(title = {
                Column {
                    Text("Buzzit", fontWeight = FontWeight.Bold)
                    if (conn != RealtimeClient.State.Connected) {
                        Text(if (conn == RealtimeClient.State.Connecting) "Connecting…" else "Waiting for network…", style = MaterialTheme.typography.labelSmall)
                    }
                }
            })
        },
        floatingActionButton = {
            ExtendedFloatingActionButton(onClick = { nav.navigate(Routes.NEW_CHAT) }, icon = { Icon(Icons.Filled.Edit, null) }, text = { Text("New chat") })
        },
    ) { pad ->
        PullToRefreshBox(
            isRefreshing = refreshing, modifier = Modifier.padding(pad).fillMaxSize(),
            onRefresh = { scope.launch { refreshing = true; runCatching { c.chats.refreshConversations() }; refreshing = false } },
        ) {
            if (conversations.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Text("No chats yet.\nAdd friends, then start a conversation.", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(32.dp))
                }
            }
            LazyColumn(Modifier.fillMaxSize()) {
                items(conversations, key = { it.id }) { conv ->
                    val peer = conv.peer(me)
                    val online = peer?.let { presence[it.id]?.online ?: it.online } == true
                    val typers = typing[conv.id].orEmpty().filter { it.value > System.currentTimeMillis() }.keys
                    ConversationRow(conv, me, online, typers.isNotEmpty()) { nav.openChat(conv.id) }
                }
            }
        }
    }
}

@Composable
private fun ConversationRow(c: ConversationEntity, me: String, online: Boolean, typing: Boolean, onClick: () -> Unit) {
    val title = c.displayTitle(me)
    val peer = c.peer(me)
    ListItem(
        modifier = Modifier.clickable(onClick = onClick),
        leadingContent = { Avatar(title, if (c.type == "group") c.avatarId else peer?.avatarId, online = online) },
        headlineContent = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (c.type == "group") Icon(Icons.Filled.Group, null, Modifier.size(16.dp).padding(end = 4.dp))
                if (c.type == "bot") Icon(Icons.Filled.SmartToy, null, Modifier.size(16.dp).padding(end = 4.dp))
                Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = if (c.unread > 0) FontWeight.Bold else FontWeight.Normal, modifier = Modifier.weight(1f))
                Text(shortTime(c.lastAt), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        },
        supportingContent = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                val text = when {
                    typing -> "typing…"
                    c.lastText == null -> "Say hi 👋"
                    c.lastSenderId == me -> "You: ${c.lastText}"
                    else -> c.lastText
                }
                Text(
                    text, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f),
                    color = if (typing) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (c.mutedUntil > System.currentTimeMillis()) Icon(Icons.Filled.NotificationsOff, "Muted", Modifier.size(16.dp))
                if (c.unread > 0) {
                    Spacer(Modifier.width(6.dp))
                    Badge(containerColor = MaterialTheme.colorScheme.primary) { Text(if (c.unread > 99) "99+" else "${c.unread}") }
                }
            }
        },
    )
}

/** Pick a friend for a 1:1 chat, or several friends for a group. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewChatScreen(nav: NavHostController, group: Boolean) {
    val c = container()
    val scope = rememberCoroutineScope()
    var friends by remember { mutableStateOf<List<Friend>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    val selected = remember { mutableStateListOf<String>() }
    var title by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }

    LaunchedEffect(Unit) {
        runCatching { c.social.friends() }.onSuccess { friends = it }.onFailure { error = it.friendly() }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(if (group) "New group" else "New chat") },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
        floatingActionButton = {
            if (group && selected.isNotEmpty() && title.isNotBlank()) {
                FloatingActionButton(onClick = {
                    if (busy) return@FloatingActionButton
                    busy = true
                    scope.launch {
                        runCatching { c.chats.createGroup(title.trim(), selected.toList()) }
                            .onSuccess { nav.popBackStack(); nav.openChat(it) }
                            .onFailure { error = it.friendly() }
                        busy = false
                    }
                }) { Icon(Icons.Filled.Check, "Create group") }
            }
        },
    ) { pad ->
        LazyColumn(Modifier.padding(pad).fillMaxSize()) {
            if (group) {
                item {
                    OutlinedTextField(
                        title, { title = it.take(100) }, label = { Text("Group name") }, singleLine = true,
                        modifier = Modifier.fillMaxWidth().padding(16.dp),
                    )
                }
            } else {
                item {
                    ListItem(
                        modifier = Modifier.clickable { nav.navigate(Routes.NEW_GROUP) },
                        leadingContent = { Icon(Icons.Filled.Group, null) },
                        headlineContent = { Text("New group") },
                    )
                    HorizontalDivider()
                }
            }
            error?.let { item { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp)) } }
            val list = friends
            if (list != null && list.isEmpty()) {
                item { Text("You don't have friends on Buzzit yet. Add some from the Friends tab.", modifier = Modifier.padding(16.dp)) }
            }
            items(list.orEmpty(), key = { it.id }) { f ->
                ListItem(
                    modifier = Modifier.clickable {
                        if (group) {
                            if (f.id in selected) selected.remove(f.id) else selected.add(f.id)
                        } else scope.launch {
                            runCatching { c.chats.openDirect(f.id) }
                                .onSuccess { nav.popBackStack(); nav.openChat(it) }
                                .onFailure { error = it.friendly() }
                        }
                    },
                    leadingContent = { Avatar(f.displayName, f.avatarId, online = f.online == true) },
                    headlineContent = { Text(f.displayName) },
                    supportingContent = { Text("@${f.username}") },
                    trailingContent = if (group) ({ Checkbox(checked = f.id in selected, onCheckedChange = null) }) else null,
                )
            }
            item { Spacer(Modifier.padding(40.dp)) }
        }
    }
}
