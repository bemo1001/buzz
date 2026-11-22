package app.buzzit.ui.home

import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.filled.People
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.navigation.NavHostController
import app.buzzit.ui.chats.ChatsTab
import app.buzzit.ui.common.container
import app.buzzit.ui.friends.FriendsTab
import app.buzzit.ui.settings.SettingsTab

@Composable
fun HomeScreen(nav: NavHostController) {
    val c = container()
    var tab by rememberSaveable { mutableIntStateOf(0) }
    val unread by c.chats.unreadTotal().collectAsState(initial = 0)
    var requests by rememberSaveable { mutableIntStateOf(0) }

    LaunchedEffect(Unit) {
        suspend fun load() { requests = runCatching { c.social.requests().incoming.size }.getOrDefault(requests) }
        load()
        c.sync.socialChanged.collect { load() }
    }

    Scaffold(
        bottomBar = {
            NavigationBar {
                NavigationBarItem(
                    selected = tab == 0, onClick = { tab = 0 }, label = { Text("Chats") },
                    icon = { BadgedBox(badge = { if ((unread ?: 0) > 0) Badge { Text("${unread}") } }) { Icon(Icons.AutoMirrored.Filled.Chat, null) } },
                )
                NavigationBarItem(
                    selected = tab == 1, onClick = { tab = 1 }, label = { Text("Friends") },
                    icon = { BadgedBox(badge = { if (requests > 0) Badge { Text("$requests") } }) { Icon(Icons.Filled.People, null) } },
                )
                NavigationBarItem(
                    selected = tab == 2, onClick = { tab = 2 }, label = { Text("Settings") },
                    icon = { Icon(Icons.Filled.Settings, null) },
                )
            }
        },
    ) { pad ->
        val m = Modifier.padding(pad)
        when (tab) {
            0 -> ChatsTab(nav, m)
            1 -> FriendsTab(nav, m)
            else -> SettingsTab(nav, m)
        }
    }
}
