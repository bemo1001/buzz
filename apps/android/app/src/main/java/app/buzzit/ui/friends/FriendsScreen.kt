package app.buzzit.ui.friends

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import app.buzzit.data.api.Friend
import app.buzzit.data.api.FriendRequests
import app.buzzit.data.api.User
import app.buzzit.openChat
import app.buzzit.ui.common.Avatar
import app.buzzit.ui.common.container
import app.buzzit.ui.common.friendly
import app.buzzit.ui.common.lastSeenText
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FriendsTab(nav: NavHostController, modifier: Modifier) {
    val c = container()
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    var friends by remember { mutableStateOf<List<Friend>>(emptyList()) }
    var requests by remember { mutableStateOf(FriendRequests()) }
    var query by remember { mutableStateOf("") }
    var results by remember { mutableStateOf<List<User>>(emptyList()) }
    var confirmBlock by remember { mutableStateOf<Friend?>(null) }
    var reload by remember { mutableStateOf(0) }

    fun act(msg: String? = null, block: suspend () -> Unit) = scope.launch {
        runCatching { block() }
            .onSuccess { msg?.let { snackbar.showSnackbar(it) }; reload++ }
            .onFailure { snackbar.showSnackbar(it.friendly()) }
    }

    LaunchedEffect(reload) {
        runCatching { friends = c.social.friends(); requests = c.social.requests() }
    }
    LaunchedEffect(Unit) { c.sync.socialChanged.collect { reload++ } }
    LaunchedEffect(query) {
        if (query.trim().length < 2) { results = emptyList(); return@LaunchedEffect }
        delay(300) // debounce
        results = runCatching { c.social.search(query.trim()) }.getOrDefault(emptyList())
    }

    val friendIds = friends.map { it.id }.toSet()
    val outgoing = requests.outgoing.map { it.user.id }.toSet()

    Scaffold(modifier = modifier, topBar = { TopAppBar(title = { Text("Friends") }) }, snackbarHost = { SnackbarHost(snackbar) }) { pad ->
        LazyColumn(Modifier.padding(pad).fillMaxSize()) {
            item {
                OutlinedTextField(
                    query, { query = it }, singleLine = true, leadingIcon = { Icon(Icons.Filled.Search, null) },
                    placeholder = { Text("Find people by username") }, modifier = Modifier.fillMaxWidth().padding(16.dp),
                )
            }
            if (results.isNotEmpty()) {
                item { Header("People") }
                items(results, key = { "s" + it.id }) { u ->
                    ListItem(
                        leadingContent = { Avatar(u.displayName, u.avatarId) },
                        headlineContent = { Text(u.displayName) },
                        supportingContent = { Text("@${u.username}" + if (u.isBot) " · bot" else "") },
                        trailingContent = {
                            when {
                                u.isBot -> TextButton(onClick = { act { nav.openChat(c.chats.openDirect(u.id)) } }) { Text("Open") }
                                u.id in friendIds -> Text("Friends", color = MaterialTheme.colorScheme.primary)
                                u.id in outgoing -> Text("Requested")
                                else -> FilledTonalButton(onClick = {
                                    act { val s = c.social.addFriend(u.id); snackbar.showSnackbar(if (s == "friends") "You're now friends" else "Request sent") }
                                }) { Icon(Icons.Filled.PersonAdd, null); Text(" Add") }
                            }
                        },
                    )
                }
            }
            if (requests.incoming.isNotEmpty()) {
                item { Header("Friend requests") }
                items(requests.incoming, key = { "in" + it.user.id }) { r ->
                    ListItem(
                        leadingContent = { Avatar(r.user.displayName, r.user.avatarId) },
                        headlineContent = { Text(r.user.displayName) },
                        supportingContent = { Text("@${r.user.username}") },
                        trailingContent = {
                            Row {
                                IconButton(onClick = { act("You're now friends with ${r.user.displayName}") { c.social.accept(r.user.id) } }) { Icon(Icons.Filled.Check, "Accept") }
                                IconButton(onClick = { act { c.social.declineOrCancel(r.user.id) } }) { Icon(Icons.Filled.Close, "Decline") }
                            }
                        },
                    )
                }
            }
            if (requests.outgoing.isNotEmpty()) {
                item { Header("Sent requests") }
                items(requests.outgoing, key = { "out" + it.user.id }) { r ->
                    ListItem(
                        leadingContent = { Avatar(r.user.displayName, r.user.avatarId) },
                        headlineContent = { Text(r.user.displayName) },
                        trailingContent = { TextButton(onClick = { act { c.social.declineOrCancel(r.user.id) } }) { Text("Cancel") } },
                    )
                }
            }
            item { Header("Your friends (${friends.size})") }
            if (friends.isEmpty()) {
                item { Text("Search above to find friends by their username.", modifier = Modifier.padding(16.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }
            }
            items(friends, key = { it.id }) { f ->
                var menu by remember { mutableStateOf(false) }
                ListItem(
                    modifier = Modifier.clickable { act { nav.openChat(c.chats.openDirect(f.id)) } },
                    leadingContent = { Avatar(f.displayName, f.avatarId, online = f.online == true) },
                    headlineContent = { Text(f.displayName) },
                    supportingContent = { Text(lastSeenText(f.online, f.lastSeenAt) ?: "@${f.username}") },
                    trailingContent = {
                        IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, "More") }
                        DropdownMenu(menu, { menu = false }) {
                            DropdownMenuItem(text = { Text("Remove friend") }, onClick = { menu = false; act("Removed") { c.social.removeFriend(f.id) } })
                            DropdownMenuItem(text = { Text("Block", color = MaterialTheme.colorScheme.error) }, onClick = { menu = false; confirmBlock = f })
                        }
                    },
                )
            }
        }
    }

    confirmBlock?.let { f ->
        AlertDialog(
            onDismissRequest = { confirmBlock = null },
            title = { Text("Block ${f.displayName}?") },
            text = { Text("They won't be able to message or buzz you, and they'll be removed from your friends. They won't be told.") },
            confirmButton = { TextButton(onClick = { confirmBlock = null; act("Blocked") { c.social.block(f.id) } }) { Text("Block") } },
            dismissButton = { TextButton(onClick = { confirmBlock = null }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun Header(text: String) {
    Text(text, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(start = 16.dp, top = 16.dp, bottom = 4.dp))
}
