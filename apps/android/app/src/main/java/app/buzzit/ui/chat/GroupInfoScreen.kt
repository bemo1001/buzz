package app.buzzit.ui.chat

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.ExitToApp
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import app.buzzit.Routes
import app.buzzit.data.api.Conversation
import app.buzzit.data.api.Friend
import app.buzzit.data.api.jsonOf
import app.buzzit.data.repo.members
import app.buzzit.ui.common.Avatar
import app.buzzit.ui.common.container
import app.buzzit.ui.common.friendly
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun GroupInfoScreen(conversationId: String, nav: NavHostController) {
    val c = container()
    val scope = rememberCoroutineScope()
    val conv by c.chats.conversation(conversationId).collectAsState(initial = null)
    var error by remember { mutableStateOf<String?>(null) }
    var rename by remember { mutableStateOf(false) }
    var adding by remember { mutableStateOf<List<Friend>?>(null) }
    var menuFor by remember { mutableStateOf<String?>(null) }
    val me = c.session.userId
    val g = conv ?: return
    val members = g.members()
    val admin = g.myRole == "owner" || g.myRole == "admin"

    fun act(block: suspend () -> Unit) = scope.launch { runCatching { block() }.onFailure { error = it.friendly() } }
    suspend fun store(conv: Conversation) = c.chats.storeConversation(conv)

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Group info") },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = { if (admin) IconButton(onClick = { rename = true }) { Icon(Icons.Filled.Edit, "Rename") } },
            )
        },
    ) { pad ->
        LazyColumn(Modifier.padding(pad).fillMaxSize()) {
            item {
                Column(Modifier.fillMaxWidth().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Avatar(g.title, g.avatarId, size = 88.dp)
                    Text(g.title, style = MaterialTheme.typography.headlineSmall)
                    Text("${members.size} members", color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            error?.let { item { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp)) } }
            if (admin) {
                item {
                    ListItem(
                        modifier = Modifier.clickable {
                            act {
                                val ids = members.map { it.user.id }.toSet()
                                adding = c.social.friends().filter { it.id !in ids }
                            }
                        },
                        leadingContent = { Icon(Icons.Filled.PersonAdd, null) },
                        headlineContent = { Text("Add members") },
                    )
                }
            }
            items(members, key = { it.user.id }) { m ->
                ListItem(
                    modifier = Modifier.clickable(enabled = m.user.id != me) { menuFor = m.user.id },
                    leadingContent = { Avatar(m.user.displayName, m.user.avatarId) },
                    headlineContent = { Text(if (m.user.id == me) "You" else m.user.displayName) },
                    supportingContent = { Text("@${m.user.username}") },
                    trailingContent = {
                        if (m.role != "member") Text(m.role.replaceFirstChar { it.uppercase() }, color = MaterialTheme.colorScheme.primary)
                        DropdownMenu(menuFor == m.user.id, { menuFor = null }) {
                            DropdownMenuItem(text = { Text("Message") }, onClick = {
                                menuFor = null
                                act { val id = c.chats.openDirect(m.user.id); nav.navigate(Routes.chat(id)) }
                            })
                            if (g.myRole == "owner" && m.role != "owner") {
                                val newRole = if (m.role == "admin") "member" else "admin"
                                DropdownMenuItem(text = { Text(if (newRole == "admin") "Make admin" else "Remove admin") }, onClick = {
                                    menuFor = null
                                    act { store(c.api.patch("/v1/conversations/$conversationId/members/${m.user.id}", jsonOf("role" to newRole))) }
                                })
                            }
                            if (admin && m.role != "owner" && !(m.role == "admin" && g.myRole != "owner")) {
                                DropdownMenuItem(text = { Text("Remove from group", color = MaterialTheme.colorScheme.error) }, onClick = {
                                    menuFor = null
                                    act { c.api.send("DELETE", "/v1/conversations/$conversationId/members/${m.user.id}"); c.chats.refreshConversation(conversationId) }
                                })
                            }
                        }
                    },
                )
            }
            item {
                HorizontalDivider()
                ListItem(
                    modifier = Modifier.clickable { act { c.chats.leave(conversationId); nav.popBackStack(Routes.HOME, false) } },
                    leadingContent = { Icon(Icons.AutoMirrored.Filled.ExitToApp, null, tint = MaterialTheme.colorScheme.error) },
                    headlineContent = { Text("Leave group", color = MaterialTheme.colorScheme.error) },
                )
            }
        }
    }

    if (rename) {
        var title by remember { mutableStateOf(g.title) }
        AlertDialog(
            onDismissRequest = { rename = false },
            title = { Text("Rename group") },
            text = { OutlinedTextField(title, { title = it.take(100) }, singleLine = true) },
            confirmButton = {
                TextButton(onClick = {
                    rename = false
                    act { store(c.api.patch("/v1/conversations/$conversationId", jsonOf("title" to title.trim()))) }
                }) { Text("Save") }
            },
            dismissButton = { TextButton(onClick = { rename = false }) { Text("Cancel") } },
        )
    }

    adding?.let { candidates ->
        val picked = remember { mutableStateListOf<String>() }
        AlertDialog(
            onDismissRequest = { adding = null },
            title = { Text("Add members") },
            text = {
                if (candidates.isEmpty()) Text("All your friends are already in this group.")
                LazyColumn {
                    items(candidates, key = { it.id }) { f ->
                        ListItem(
                            modifier = Modifier.clickable { if (f.id in picked) picked.remove(f.id) else picked.add(f.id) },
                            headlineContent = { Text(f.displayName) },
                            trailingContent = { Checkbox(f.id in picked, null) },
                        )
                    }
                }
            },
            confirmButton = {
                TextButton(enabled = picked.isNotEmpty(), onClick = {
                    val ids = picked.toList()
                    adding = null
                    act { store(c.api.post("/v1/conversations/$conversationId/members", jsonOf("user_ids" to ids))) }
                }) { Text("Add") }
            },
            dismissButton = { TextButton(onClick = { adding = null }) { Text("Cancel") } },
        )
    }
}
