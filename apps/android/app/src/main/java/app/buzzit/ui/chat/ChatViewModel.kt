package app.buzzit.ui.chat

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import app.buzzit.BuzzitApp
import app.buzzit.data.api.Buzz
import app.buzzit.data.api.Friend
import app.buzzit.data.db.ConversationEntity
import app.buzzit.data.db.MessageEntity
import app.buzzit.data.repo.OpenConversation
import app.buzzit.data.repo.peer
import kotlinx.coroutines.flow.firstOrNull
import app.buzzit.ui.common.friendly
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

@OptIn(ExperimentalCoroutinesApi::class)
class ChatViewModel(app: Application, val conversationId: String) : AndroidViewModel(app) {
    private val c = (app as BuzzitApp).container
    val me = c.session.userId

    val conversation: StateFlow<ConversationEntity?> = c.chats.conversation(conversationId)
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)

    private val limit = MutableStateFlow(60)
    val messages: StateFlow<List<MessageEntity>> = limit.flatMapLatest { c.chats.messages(conversationId, it) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    val typing = c.sync.typing
    val presence = c.sync.presence
    val buzzUpdates = c.buzzUpdates

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error

    /** What the peer allows me to buzz them with (1:1 chats only). */
    private val _buzzLevel = MutableStateFlow("nudge")
    val buzzLevel: StateFlow<String> = _buzzLevel

    private var hasMore = true
    private var loadingOlder = false
    private var lastTypingSent = 0L

    init {
        viewModelScope.launch {
            runCatching { c.chats.refreshConversation(conversationId) }
            runCatching { c.chats.refreshMessages(conversationId) }.onFailure { _error.value = it.friendly() }
            c.chats.markRead(conversationId)
            refreshBuzzLevel()
        }
        viewModelScope.launch { c.sync.socialChanged.collect { refreshBuzzLevel() } }
        viewModelScope.launch {
            // Keep the open conversation marked read as new messages arrive.
            messages.collect { list ->
                val newest = list.firstOrNull() ?: return@collect
                if (newest.senderId != me && (conversation.value?.unread ?: 0) > 0) c.chats.markRead(conversationId)
            }
        }
    }

    private suspend fun refreshBuzzLevel() {
        val conv = c.chats.conversation(conversationId).firstOrNull() ?: return
        if (conv.type != "direct") return
        val other = conv.peer(me)?.id ?: return
        val friends: List<Friend> = runCatching { c.social.friends() }.getOrDefault(emptyList())
        friends.firstOrNull { it.id == other }?.let { _buzzLevel.value = it.canBuzzLevel }
    }

    fun onOpen() { OpenConversation.id = conversationId; c.notifier.cancelConversation(conversationId) }
    fun onClose() { if (OpenConversation.id == conversationId) OpenConversation.id = null }

    fun clearError() { _error.value = null }

    fun loadOlder() {
        if (!hasMore || loadingOlder) return
        loadingOlder = true
        viewModelScope.launch {
            runCatching { hasMore = c.chats.loadOlder(conversationId) }
            limit.value += 60
            loadingOlder = false
        }
    }

    fun sendText(text: String, replyTo: String?) = viewModelScope.launch { c.chats.sendText(conversationId, text.trim(), replyTo) }

    fun sendMedia(kind: String, uri: String, mime: String, name: String) =
        viewModelScope.launch { c.chats.sendMedia(conversationId, kind, uri, mime, name, null) }

    fun typing() {
        val now = System.currentTimeMillis()
        if (now - lastTypingSent > 3_000) {
            lastTypingSent = now
            c.realtime.sendTyping(conversationId)
        }
    }

    private fun act(block: suspend () -> Unit) = viewModelScope.launch { runCatching { block() }.onFailure { _error.value = it.friendly() } }

    fun edit(m: MessageEntity, text: String) = act { c.chats.edit(conversationId, m.id, text.trim()) }
    fun delete(m: MessageEntity) = act {
        if (m.id.startsWith("local_")) c.chats.discard(m.id) else c.chats.delete(conversationId, m.id)
    }
    fun react(m: MessageEntity, emoji: String?) = act { c.chats.react(conversationId, m.id, emoji) }
    fun retry(m: MessageEntity) = act { c.chats.retry(m.id) }
    fun setMuted(muted: Boolean) = act { c.chats.setMuted(conversationId, muted) }
    fun leave(done: () -> Unit) = act { c.chats.leave(conversationId); done() }

    fun buzz(recipient: String, level: String, note: String?, done: (Buzz?) -> Unit) = viewModelScope.launch {
        runCatching { c.api.buzz(recipient, level, note?.takeIf { it.isNotBlank() }) }
            .onSuccess { done(it) }
            .onFailure { _error.value = it.friendly(); done(null) }
    }

    fun mediaUrl(id: String) = c.api.mediaUrl(id)
    fun token() = c.session.token.orEmpty()

    class Factory(private val app: Application, private val id: String) : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>): T = ChatViewModel(app, id) as T
    }
}
