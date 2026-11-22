package app.buzzit

import app.buzzit.data.api.ApiClient
import app.buzzit.data.api.ApiException
import app.buzzit.data.api.BuzzEvent
import app.buzzit.data.api.BuzzPass
import app.buzzit.data.api.BuzzitJson
import app.buzzit.data.api.Conversation
import app.buzzit.data.api.FriendsResponse
import app.buzzit.data.api.Message
import app.buzzit.data.api.StatusResponse
import app.buzzit.data.api.jsonOf
import app.buzzit.data.session.Credentials
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * Runs the Android API layer against a real buzzit-server, checking that our
 * Kotlin models decode the server's actual JSON. Skipped unless
 * BUZZIT_TEST_SERVER is set, e.g.:
 *
 *   buzzit-server --addr :8095 --data /tmp/bz &
 *   BUZZIT_TEST_SERVER=http://127.0.0.1:8095 ./gradlew testDebugUnitTest
 */
class ServerContractTest {

    private class Creds(override val serverUrl: String) : Credentials {
        override var token: String? = null
    }

    private fun client(base: String) = Creds(base).let { it to ApiClient(it) }

    @Test fun fullFlowAgainstRealServer() = runTest {
        val base = System.getenv("BUZZIT_TEST_SERVER")
        assumeTrue("BUZZIT_TEST_SERVER not set", !base.isNullOrEmpty())
        val suffix = System.nanoTime().toString().takeLast(6)

        val (aCreds, alice) = client(base!!)
        val (bCreds, bob) = client(base)
        val a = alice.register("alice$suffix", "password123", "Alice", "test")
        aCreds.token = a.token
        val b = bob.register("bob$suffix", "password123", "Bob", "test")
        bCreds.token = b.token

        assertEquals("alice$suffix", alice.me().username)

        val st: StatusResponse = bob.post("/v1/friends/requests", jsonOf("username" to "alice$suffix"))
        assertEquals("request_sent", st.status)
        alice.post<StatusResponse>("/v1/friends/requests/${b.userId}/accept")
        val friends = bob.get<FriendsResponse>("/v1/friends").friends
        assertEquals("nudge", friends.single().canBuzzLevel)

        val conv: Conversation = bob.post("/v1/conversations", jsonOf("type" to "direct", "user_id" to a.userId))
        assertEquals(2, conv.members.size)
        val sent = bob.sendMessage(conv.id, "c-1", "text", "hello from kotlin", null, null)
        assertEquals("hello from kotlin", sent.text)

        val list = alice.conversations()
        assertEquals(1, list.single().unreadCount)
        assertEquals("hello from kotlin", list.single().lastMessage?.text)
        val page = alice.messages(conv.id)
        assertEquals(sent.id, page.messages.first().id)

        // Events decode through the same path the sync engine uses.
        val events = alice.sync(0).events
        val msgEvent = events.first { it.type == "message.new" }
        assertEquals(sent.id, BuzzitJson.decodeFromJsonElement(Message.serializer(), msgEvent.data).id)

        try {
            bob.buzz(a.userId, "emergency", null)
            throw AssertionError("emergency buzz should need a pass")
        } catch (e: ApiException) {
            assertEquals("level_not_granted", e.code)
        }
        val pass: BuzzPass = alice.put("/v1/buzz-passes/${b.userId}", jsonOf("max_level" to "emergency", "quiet_start_min" to null, "quiet_end_min" to null, "tz" to "Europe/Paris"))
        assertEquals("emergency", pass.maxLevel)
        val bz = bob.buzz(a.userId, "emergency", "call me")
        assertEquals("sent", bz.status)
        assertTrue(bz.createdAtMillis() > 0)

        val buzzEvent = alice.sync(0).events.last { it.type == "buzz" }
        val be = BuzzitJson.decodeFromJsonElement(BuzzEvent.serializer(), buzzEvent.data)
        assertEquals(bz.id, be.buzz.id)
        assertEquals("Bob", be.sender.displayName)
        alice.buzzStatus(bz.id, "acked")
    }
}
