package app.buzzit

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import app.buzzit.buzz.BuzzAlertService
import app.buzzit.data.api.ApiClient
import app.buzzit.data.api.Buzz
import app.buzzit.data.api.BuzzEvent
import app.buzzit.data.api.Message
import app.buzzit.data.api.RealtimeClient
import app.buzzit.data.api.UserRef
import app.buzzit.data.db.BuzzitDatabase
import app.buzzit.data.db.ConversationEntity
import app.buzzit.data.repo.ChatRepository
import app.buzzit.data.repo.MediaUploader
import app.buzzit.data.repo.OpenConversation
import app.buzzit.data.repo.SyncEffects
import app.buzzit.data.repo.SyncEngine
import app.buzzit.data.repo.SocialRepository
import app.buzzit.data.session.SessionStore
import app.buzzit.service.ConnectionService
import app.buzzit.service.DeviceRegistration
import app.buzzit.service.Notifier
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.launch

/** Manual dependency container; one per process. */
class AppContainer(private val context: Context) : SyncEffects {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    val session = SessionStore(context)
    val api = ApiClient(session)
    val db = BuzzitDatabase.create(context)
    val chats = ChatRepository(context, api, db, session)
    val social = SocialRepository(api)
    val uploader = MediaUploader(context, api)
    val notifier = Notifier(context)
    val sync = SyncEngine(api, db, session, chats, this)
    val realtime = RealtimeClient(api, session, scope) { sync.onSocket(it) }

    private val _buzzUpdates = MutableSharedFlow<Buzz>(extraBufferCapacity = 8)
    /** Status changes of buzzes I sent (for in-app feedback). */
    val buzzUpdates: SharedFlow<Buzz> = _buzzUpdates

    @Volatile var inForeground = false
        private set

    init {
        api.onUnauthorized = { scope.launch { signOutLocal() } }
        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) {
                inForeground = true
                if (session.token != null) {
                    realtime.start()
                    scope.launch { runCatching { sync.catchUp() } }
                }
            }

            override fun onStop(owner: LifecycleOwner) {
                inForeground = false
                if (session.token == null) return
                if (session.keepConnected) ConnectionService.start(context) else realtime.stop()
            }
        })
        context.getSystemService(ConnectivityManager::class.java).registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = realtime.kick()
        })
    }

    /** Called after login/register. */
    suspend fun onSignedIn() {
        runCatching { chats.refreshConversations() }
        realtime.start()
        runCatching { sync.catchUp() }
        DeviceRegistration.syncPushToken(context)
        chats.flushOutbox()
    }

    suspend fun signOut() {
        runCatching { api.send("POST", "/v1/auth/logout") }
        signOutLocal()
    }

    private suspend fun signOutLocal() {
        if (session.token == null) return
        realtime.stop()
        ConnectionService.stop(context)
        session.signOut()
        chats.clearLocal()
        notifier.cancelAll()
    }

    // ---- SyncEffects --------------------------------------------------------

    override fun incomingBuzz(ev: BuzzEvent) = BuzzAlertService.start(context, ev.buzz, ev.sender)

    override fun buzzStatusChanged(b: Buzz, iAmRecipient: Boolean) {
        if (iAmRecipient) {
            // Answered on another device (or this one): stop ringing here.
            if (b.status == "acked") BuzzAlertService.dismiss(context, b.id)
        } else {
            _buzzUpdates.tryEmit(b)
            if (b.status == "acked" && !inForeground) notifier.buzzAcknowledged(b)
        }
    }

    override fun incomingMessage(m: Message, conversation: ConversationEntity?) {
        if (conversation == null) return
        if (conversation.mutedUntil > System.currentTimeMillis()) return
        if (inForeground && OpenConversation.id == m.conversationId) return
        notifier.message(m, conversation, session.userId)
    }

    override fun friendRequest(ref: UserRef) {
        ref.user?.let { notifier.friendRequest(it) }
    }
}
