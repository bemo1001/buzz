package app.buzzit.ui.settings

import android.content.Intent
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Logout
import androidx.compose.material.icons.filled.Apps
import androidx.compose.material.icons.filled.Block
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Devices
import androidx.compose.material.icons.filled.Error
import androidx.compose.material.icons.filled.Key
import androidx.compose.material.icons.filled.NotificationsActive
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import androidx.navigation.NavHostController
import app.buzzit.Routes
import app.buzzit.buzz.BuzzPermissions
import app.buzzit.data.api.BuzzPass
import app.buzzit.data.api.ConnectedApp
import app.buzzit.data.api.Device
import app.buzzit.data.api.Friend
import app.buzzit.data.api.Me
import app.buzzit.data.api.User
import app.buzzit.service.ConnectionService
import app.buzzit.ui.chat.fileName
import app.buzzit.ui.common.Avatar
import app.buzzit.ui.common.container
import app.buzzit.ui.common.friendly
import app.buzzit.ui.common.shortTime
import kotlinx.coroutines.launch
import java.util.TimeZone

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsTab(nav: NavHostController, modifier: Modifier) {
    val c = container()
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    var me by remember { mutableStateOf<Me?>(null) }
    var editName by remember { mutableStateOf(false) }
    var changePw by remember { mutableStateOf(false) }
    var keep by remember { mutableStateOf(c.session.keepConnected) }

    fun act(msg: String? = null, block: suspend () -> Unit) = scope.launch {
        runCatching { block() }.onSuccess { msg?.let { snackbar.showSnackbar(it) } }.onFailure { snackbar.showSnackbar(it.friendly()) }
    }
    LaunchedEffect(Unit) { runCatching { me = c.social.me() } }

    val pickAvatar = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        uri ?: return@rememberLauncherForActivityResult
        act("Profile photo updated") {
            val m = c.uploader.upload(uri.toString(), ctx.contentResolver.getType(uri) ?: "image/jpeg", ctx.fileName(uri), "image")
            me = c.social.updateMe("avatar_media_id" to m.id)
        }
    }

    Scaffold(modifier = modifier, topBar = { TopAppBar(title = { Text("Settings") }) }, snackbarHost = { SnackbarHost(snackbar) }) { pad ->
        LazyColumn(Modifier.padding(pad).fillMaxSize()) {
            me?.let { m ->
                item {
                    Row(Modifier.fillMaxWidth().padding(16.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        Avatar(m.displayName, m.avatarId, size = 72.dp, modifier = Modifier.clickable { pickAvatar.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) })
                        Column(Modifier.weight(1f)) {
                            Text(m.displayName, style = MaterialTheme.typography.titleLarge)
                            Text("@${m.username}", color = MaterialTheme.colorScheme.onSurfaceVariant)
                            if (m.bio.isNotEmpty()) Text(m.bio, style = MaterialTheme.typography.bodySmall)
                        }
                        TextButton(onClick = { editName = true }) { Text("Edit") }
                    }
                    HorizontalDivider()
                }
                item { Section("Privacy") }
                item {
                    ToggleRow("Read receipts", "Let people see when you've read their messages", m.sendReadReceipts) { v ->
                        act { me = c.social.updateMe("send_read_receipts" to v) }
                    }
                }
                item {
                    ToggleRow("Online status & last seen", "Let friends see when you're online", m.showLastSeen) { v ->
                        act { me = c.social.updateMe("show_last_seen" to v) }
                    }
                }
            }
            item { Section("Buzz") }
            item {
                ListItem(
                    modifier = Modifier.clickable { nav.navigate(Routes.BUZZ_SETTINGS) },
                    leadingContent = { Icon(Icons.Filled.NotificationsActive, null) },
                    headlineContent = { Text("Who can buzz me") },
                    supportingContent = { Text("Buzz levels per friend, quiet hours, and phone permissions") },
                )
            }
            item { Section("Connection") }
            item {
                ToggleRow("Stay connected in background", "Instant messages and buzzes without Google push. Shows a small ongoing notification.", keep) { v ->
                    keep = v
                    c.session.keepConnected = v
                    if (!v) ConnectionService.stop(ctx)
                }
            }
            item {
                ListItem(headlineContent = { Text("Server") }, supportingContent = { Text(c.session.serverUrl) })
            }
            item { Section("Account") }
            item { NavRow(Icons.Filled.Devices, "Devices", "Where you're signed in") { nav.navigate(Routes.DEVICES) } }
            item { NavRow(Icons.Filled.Apps, "Connected apps", "Apps using the Buzzit Platform for you") { nav.navigate(Routes.APPS) } }
            item { NavRow(Icons.Filled.Block, "Blocked people", null) { nav.navigate(Routes.BLOCKED) } }
            item { NavRow(Icons.Filled.Key, "Change password", "Signs out your other devices") { changePw = true } }
            item {
                ListItem(
                    modifier = Modifier.clickable { act { c.signOut() } },
                    leadingContent = { Icon(Icons.AutoMirrored.Filled.Logout, null, tint = MaterialTheme.colorScheme.error) },
                    headlineContent = { Text("Sign out", color = MaterialTheme.colorScheme.error) },
                )
            }
        }
    }

    if (editName) me?.let { m ->
        var name by remember { mutableStateOf(m.displayName) }
        var bio by remember { mutableStateOf(m.bio) }
        AlertDialog(
            onDismissRequest = { editName = false },
            title = { Text("Edit profile") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(name, { name = it.take(64) }, label = { Text("Name") }, singleLine = true)
                    OutlinedTextField(bio, { bio = it.take(280) }, label = { Text("Bio") })
                }
            },
            confirmButton = { TextButton(onClick = { editName = false; act { me = c.social.updateMe("display_name" to name.trim(), "bio" to bio) } }) { Text("Save") } },
            dismissButton = { TextButton(onClick = { editName = false }) { Text("Cancel") } },
        )
    }

    if (changePw) {
        var cur by remember { mutableStateOf("") }
        var new by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { changePw = false },
            title = { Text("Change password") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(cur, { cur = it }, label = { Text("Current password") }, singleLine = true, visualTransformation = PasswordVisualTransformation())
                    OutlinedTextField(new, { new = it }, label = { Text("New password (8+ characters)") }, singleLine = true, visualTransformation = PasswordVisualTransformation())
                }
            },
            confirmButton = {
                TextButton(enabled = new.length >= 8, onClick = { changePw = false; act("Password changed") { c.social.changePassword(cur, new) } }) { Text("Change") }
            },
            dismissButton = { TextButton(onClick = { changePw = false }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun Section(title: String) = Text(
    title, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.primary,
    modifier = Modifier.padding(start = 16.dp, top = 20.dp, bottom = 4.dp),
)

@Composable
private fun ToggleRow(title: String, detail: String, value: Boolean, onChange: (Boolean) -> Unit) = ListItem(
    modifier = Modifier.clickable { onChange(!value) },
    headlineContent = { Text(title) },
    supportingContent = { Text(detail) },
    trailingContent = { Switch(value, onChange) },
)

@Composable
private fun NavRow(icon: androidx.compose.ui.graphics.vector.ImageVector, title: String, detail: String?, onClick: () -> Unit) = ListItem(
    modifier = Modifier.clickable(onClick = onClick),
    leadingContent = { Icon(icon, null) },
    headlineContent = { Text(title) },
    supportingContent = detail?.let { { Text(it) } },
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SubScreen(title: String, nav: NavHostController, snackbar: SnackbarHostState? = null, content: @Composable (Modifier) -> Unit) {
    Scaffold(
        topBar = {
            TopAppBar(title = { Text(title) }, navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } })
        },
        snackbarHost = { snackbar?.let { SnackbarHost(it) } },
    ) { pad -> content(Modifier.padding(pad).fillMaxSize()) }
}

// ---- Buzz settings ---------------------------------------------------------

private val levelNames = linkedMapOf("nudge" to "Nudge only", "buzz" to "Buzz (rings on silent)", "emergency" to "Emergency (rings until I answer)")

@Composable
fun BuzzSettingsScreen(nav: NavHostController) {
    val c = container()
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    var friends by remember { mutableStateOf<List<Friend>>(emptyList()) }
    var passes by remember { mutableStateOf<Map<String, BuzzPass>>(emptyMap()) }
    var perms by remember { mutableStateOf(BuzzPermissions.check(ctx)) }
    var quietFor by remember { mutableStateOf<Friend?>(null) }
    var reload by remember { mutableIntStateOf(0) }
    val lifecycle = LocalLifecycleOwner.current

    LaunchedEffect(reload) {
        runCatching {
            friends = c.social.friends()
            passes = c.social.passes().associateBy { it.userId }
        }
    }
    // Re-check permissions when coming back from system settings.
    LaunchedEffect(Unit) { lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) { perms = BuzzPermissions.check(ctx) } }

    fun act(block: suspend () -> Unit) = scope.launch {
        runCatching { block() }.onSuccess { reload++ }.onFailure { snackbar.showSnackbar(it.friendly()) }
    }

    SubScreen("Who can buzz me", nav, snackbar) { m ->
        LazyColumn(m) {
            item { Section("Make sure buzzes get through") }
            item { PermRow("Notifications", perms.notifications) { ctx.startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, ctx.packageName)) } }
            item { PermRow("Ring through Do Not Disturb", perms.dndBypass) { ctx.startActivity(BuzzPermissions.dndSettingsIntent()) } }
            item { PermRow("Show full-screen alerts", perms.fullScreen) { BuzzPermissions.fullScreenSettingsIntent(ctx)?.let(ctx::startActivity) } }
            item {
                Text(
                    "Buzzes play on your alarm volume, so they ring even when your phone is on silent or vibrate. Nobody can send you more than you allow below.",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(16.dp),
                )
            }
            item { Section("Friends") }
            if (friends.isEmpty()) item { Text("Add friends to choose who can buzz you.", modifier = Modifier.padding(16.dp)) }
            items(friends, key = { it.id }) { f ->
                val pass = passes[f.id]
                val level = pass?.maxLevel ?: "nudge"
                var menu by remember { mutableStateOf(false) }
                ListItem(
                    modifier = Modifier.clickable { menu = true },
                    leadingContent = { Avatar(f.displayName, f.avatarId, size = 40.dp) },
                    headlineContent = { Text(f.displayName) },
                    supportingContent = {
                        val quiet = pass?.quietStartMin?.let { s -> " · quiet ${hm(s)}–${hm(pass.quietEndMin ?: 0)}" } ?: ""
                        Text(levelNames[level] + quiet, color = if (level == "emergency") Color(0xFFD32F2F) else MaterialTheme.colorScheme.onSurfaceVariant)
                    },
                    trailingContent = {
                        DropdownMenu(menu, { menu = false }) {
                            levelNames.forEach { (wire, label) ->
                                DropdownMenuItem(text = { Text(label) }, onClick = {
                                    menu = false
                                    act {
                                        if (wire == "nudge" && pass?.quietStartMin == null) c.social.removePass(f.id)
                                        else c.social.setPass(f.id, wire, pass?.quietStartMin, pass?.quietEndMin, TimeZone.getDefault().id)
                                    }
                                })
                            }
                            HorizontalDivider()
                            DropdownMenuItem(text = { Text("Quiet hours…") }, onClick = { menu = false; quietFor = f })
                        }
                    },
                )
            }
        }
    }

    quietFor?.let { f ->
        val pass = passes[f.id]
        var start by remember { mutableIntStateOf(pass?.quietStartMin ?: 22 * 60) }
        var end by remember { mutableIntStateOf(pass?.quietEndMin ?: 7 * 60) }
        AlertDialog(
            onDismissRequest = { quietFor = null },
            title = { Text("Quiet hours for ${f.displayName}") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("During quiet hours only Emergency buzzes get through.")
                    HourPicker("From", start) { start = it }
                    HourPicker("Until", end) { end = it }
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    quietFor = null
                    act { c.social.setPass(f.id, pass?.maxLevel ?: "nudge", start, end, TimeZone.getDefault().id) }
                }) { Text("Save") }
            },
            dismissButton = {
                TextButton(onClick = {
                    quietFor = null
                    act {
                        val lvl = pass?.maxLevel ?: "nudge"
                        if (lvl == "nudge") c.social.removePass(f.id) else c.social.setPass(f.id, lvl, null, null, TimeZone.getDefault().id)
                    }
                }) { Text("No quiet hours") }
            },
        )
    }
}

private fun hm(min: Int) = "%02d:%02d".format(min / 60, min % 60)

@Composable
private fun HourPicker(label: String, value: Int, onChange: (Int) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(label, Modifier.weight(1f))
        OutlinedButton(onClick = { open = true }) { Text(hm(value)) }
        DropdownMenu(open, { open = false }) {
            (0 until 48).forEach { i ->
                DropdownMenuItem(text = { Text(hm(i * 30)) }, onClick = { open = false; onChange(i * 30) })
            }
        }
    }
}

@Composable
private fun PermRow(title: String, ok: Boolean, fix: () -> Unit) = ListItem(
    modifier = Modifier.clickable(enabled = !ok, onClick = fix),
    leadingContent = {
        Icon(if (ok) Icons.Filled.CheckCircle else Icons.Filled.Error, null, tint = if (ok) Color(0xFF43A047) else MaterialTheme.colorScheme.error)
    },
    headlineContent = { Text(title) },
    trailingContent = { if (!ok) TextButton(onClick = fix) { Text("Allow") } },
)

// ---- devices, apps, blocked ------------------------------------------------

@Composable
fun DevicesScreen(nav: NavHostController) {
    val c = container()
    val scope = rememberCoroutineScope()
    var devices by remember { mutableStateOf<List<Device>>(emptyList()) }
    var reload by remember { mutableIntStateOf(0) }
    LaunchedEffect(reload) { runCatching { devices = c.social.devices() } }
    SubScreen("Devices", nav) { m ->
        LazyColumn(m) {
            items(devices, key = { it.id }) { d ->
                ListItem(
                    headlineContent = { Text(d.name.ifEmpty { d.platform.ifEmpty { "Unknown device" } } + if (d.current) " (this device)" else "") },
                    supportingContent = { Text("Last active ${shortTime(d.lastUsedAt)}") },
                    trailingContent = {
                        if (!d.current) TextButton(onClick = { scope.launch { runCatching { c.social.removeDevice(d.id) }; reload++ } }) { Text("Sign out") }
                    },
                )
            }
        }
    }
}

@Composable
fun ConnectedAppsScreen(nav: NavHostController) {
    val c = container()
    val scope = rememberCoroutineScope()
    var apps by remember { mutableStateOf<List<ConnectedApp>>(emptyList()) }
    var reload by remember { mutableIntStateOf(0) }
    LaunchedEffect(reload) { runCatching { apps = c.social.connectedApps() } }
    SubScreen("Connected apps", nav) { m ->
        LazyColumn(m) {
            if (apps.isEmpty()) item {
                Text("No apps are connected. Apps built on the free Buzzit Platform can message or nudge you only after you allow them.", modifier = Modifier.padding(16.dp))
            }
            items(apps, key = { it.app.id }) { a ->
                ListItem(
                    leadingContent = { Avatar(a.app.name, null, size = 40.dp) },
                    headlineContent = { Text(a.app.name) },
                    supportingContent = { Text(a.scopes.joinToString { scopeLabel(it) }) },
                    trailingContent = { TextButton(onClick = { scope.launch { runCatching { c.social.revokeApp(a.app.id) }; reload++ } }) { Text("Remove") } },
                )
            }
        }
    }
}

private fun scopeLabel(s: String) = when (s) {
    "profile:read" -> "see your profile"
    "messages:send" -> "message you"
    "buzz:send" -> "nudge/buzz you"
    else -> s
}

@Composable
fun BlockedScreen(nav: NavHostController) {
    val c = container()
    val scope = rememberCoroutineScope()
    var blocked by remember { mutableStateOf<List<User>>(emptyList()) }
    var reload by remember { mutableIntStateOf(0) }
    LaunchedEffect(reload) { runCatching { blocked = c.social.blocked() } }
    SubScreen("Blocked people", nav) { m ->
        LazyColumn(m) {
            if (blocked.isEmpty()) item { Text("You haven't blocked anyone.", modifier = Modifier.padding(16.dp)) }
            items(blocked, key = { it.id }) { u ->
                ListItem(
                    leadingContent = { Avatar(u.displayName, u.avatarId, size = 40.dp) },
                    headlineContent = { Text(u.displayName) },
                    supportingContent = { Text("@${u.username}") },
                    trailingContent = { TextButton(onClick = { scope.launch { runCatching { c.social.unblock(u.id) }; reload++ } }) { Text("Unblock") } },
                )
            }
        }
    }
}
