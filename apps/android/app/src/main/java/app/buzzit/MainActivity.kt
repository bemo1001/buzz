package app.buzzit

import android.Manifest
import android.content.Intent
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import app.buzzit.ui.auth.AuthScreen
import app.buzzit.ui.chat.ChatScreen
import app.buzzit.ui.chat.GroupInfoScreen
import app.buzzit.ui.chats.NewChatScreen
import app.buzzit.ui.common.container
import app.buzzit.ui.home.HomeScreen
import app.buzzit.ui.settings.BlockedScreen
import app.buzzit.ui.settings.BuzzSettingsScreen
import app.buzzit.ui.settings.ConnectedAppsScreen
import app.buzzit.ui.settings.DevicesScreen
import app.buzzit.ui.theme.BuzzitTheme
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {

    /** Deep link requested by a notification or the buzz screen. */
    private val pending = mutableStateOf<Intent?>(null)

    private val askNotifications = registerForActivityResult(ActivityResultContracts.RequestPermission()) {}

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        pending.value = intent
        if (Build.VERSION.SDK_INT >= 33) askNotifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        setContent { BuzzitTheme { Root(pending.value) { pending.value = null } } }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        pending.value = intent
    }

    companion object {
        const val EXTRA_CONVERSATION = "conversation_id"
        const val EXTRA_OPEN_USER = "open_user_id"
    }
}

object Routes {
    const val HOME = "home"
    const val CHAT = "chat/{id}"
    const val GROUP_INFO = "group/{id}"
    const val NEW_CHAT = "new_chat"
    const val NEW_GROUP = "new_group"
    const val BUZZ_SETTINGS = "buzz_settings"
    const val DEVICES = "devices"
    const val APPS = "apps"
    const val BLOCKED = "blocked"
    fun chat(id: String) = "chat/$id"
    fun groupInfo(id: String) = "group/$id"
}

@Composable
private fun Root(deepLink: Intent?, consumed: () -> Unit) {
    val c = container()
    val loggedIn by c.session.loggedIn.collectAsState()
    if (!loggedIn) {
        AuthScreen()
        return
    }
    val nav = rememberNavController()
    val scope = rememberCoroutineScope()

    LaunchedEffect(deepLink) {
        val intent = deepLink ?: return@LaunchedEffect
        intent.getStringExtra(MainActivity.EXTRA_CONVERSATION)?.let { nav.openChat(it) }
        intent.getStringExtra(MainActivity.EXTRA_OPEN_USER)?.let { uid ->
            scope.launch { runCatching { c.chats.openDirect(uid) }.onSuccess { nav.openChat(it) } }
        }
        consumed()
    }

    NavHost(nav, startDestination = Routes.HOME) {
        composable(Routes.HOME) { HomeScreen(nav) }
        composable(Routes.CHAT, arguments = listOf(navArgument("id") { type = NavType.StringType })) {
            ChatScreen(it.arguments!!.getString("id")!!, nav)
        }
        composable(Routes.GROUP_INFO, arguments = listOf(navArgument("id") { type = NavType.StringType })) {
            GroupInfoScreen(it.arguments!!.getString("id")!!, nav)
        }
        composable(Routes.NEW_CHAT) { NewChatScreen(nav, group = false) }
        composable(Routes.NEW_GROUP) { NewChatScreen(nav, group = true) }
        composable(Routes.BUZZ_SETTINGS) { BuzzSettingsScreen(nav) }
        composable(Routes.DEVICES) { DevicesScreen(nav) }
        composable(Routes.APPS) { ConnectedAppsScreen(nav) }
        composable(Routes.BLOCKED) { BlockedScreen(nav) }
    }
}

fun NavHostController.openChat(id: String) = navigate(Routes.chat(id)) {
    popUpTo(Routes.HOME)
    launchSingleTop = true
}
