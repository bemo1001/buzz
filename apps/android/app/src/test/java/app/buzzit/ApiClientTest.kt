package app.buzzit

import app.buzzit.data.api.ApiClient
import app.buzzit.data.api.ApiException
import app.buzzit.data.api.BuzzitJson
import app.buzzit.data.api.Message
import app.buzzit.data.api.WsEvent
import app.buzzit.data.api.jsonOf
import app.buzzit.data.repo.previewText
import app.buzzit.data.session.Credentials
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test

class ApiClientTest {
    private lateinit var server: MockWebServer
    private lateinit var api: ApiClient

    private class Creds(override val serverUrl: String, override val token: String?) : Credentials

    @Before fun setUp() {
        server = MockWebServer().apply { start() }
        api = ApiClient(Creds(server.url("/").toString().trimEnd('/'), "bzu_test"))
    }

    @After fun tearDown() = server.shutdown()

    @Test fun sendsBearerTokenAndParsesSession() = runTest {
        server.enqueue(MockResponse().setBody("""{"token":"bzu_x","user_id":"usr_1","device_id":"dev_1","extra":"ignored"}"""))
        val s = api.login("alice", "pw", "Pixel")
        assertEquals("usr_1", s.userId)
        val req = server.takeRequest()
        assertEquals("Bearer bzu_test", req.getHeader("Authorization"))
        assertTrue(req.body.readUtf8().contains("\"platform\":\"android\""))
    }

    @Test fun mapsServerErrors() = runTest {
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":{"code":"level_not_granted","message":"buzz: recipient has not granted this level"}}"""))
        try {
            api.buzz("usr_2", "emergency", null)
            fail("expected ApiException")
        } catch (e: ApiException) {
            assertEquals(403, e.status)
            assertEquals("level_not_granted", e.code)
        }
    }

    @Test fun unauthorizedTriggersCallback() = runTest {
        var called = false
        api.onUnauthorized = { called = true }
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":{"code":"unauthenticated","message":"authentication required"}}"""))
        runCatching { api.me() }
        assertTrue(called)
    }

    @Test fun jsonOfEncodesValues() {
        val o = jsonOf("a" to "x", "n" to 3, "b" to true, "l" to listOf("p", "q"), "z" to null)
        assertEquals("""{"a":"x","n":3,"b":true,"l":["p","q"],"z":null}""", o.toString())
    }

    @Test fun decodesServerEventsAndPreviews() {
        val ev = BuzzitJson.decodeFromString<WsEvent>(
            """{"seq":7,"type":"message.new","data":{"id":"msg_1","conversation_id":"cnv_1","seq":3,"sender_id":"usr_1","kind":"image",
               "media":{"id":"med_1","mime":"image/png","size":10,"filename":"a.png"},"reactions":[],"deleted":false,"created_at":1}}""",
        )
        val m = BuzzitJson.decodeFromJsonElement(Message.serializer(), ev.data)
        assertEquals(7, ev.seq)
        assertEquals("📷 Photo", m.previewText())
        assertEquals("Message deleted", m.copy(deleted = true).previewText())
    }
}
