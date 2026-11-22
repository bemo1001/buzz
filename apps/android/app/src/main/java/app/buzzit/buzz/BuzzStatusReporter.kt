package app.buzzit.buzz

import android.content.Context
import android.util.Log
import app.buzzit.BuzzitApp
import kotlinx.coroutines.launch

/** Tells the server how far a buzz got: delivered -> rang -> acked. */
object BuzzStatusReporter {
    fun report(ctx: Context, buzzId: String, status: String) {
        val c = (ctx.applicationContext as BuzzitApp).container
        if (c.session.token == null) return
        c.scope.launch {
            runCatching { c.api.buzzStatus(buzzId, status) }.onFailure { Log.w("Buzz", "status $status failed: ${it.message}") }
        }
    }
}
