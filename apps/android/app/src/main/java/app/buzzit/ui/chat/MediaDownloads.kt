package app.buzzit.ui.chat

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.widget.Toast
import androidx.core.content.FileProvider
import app.buzzit.BuzzitApp
import app.buzzit.data.api.await
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.Request
import java.io.File

/** Downloads private media with our auth header and opens it in another app. */
object MediaDownloads {
    fun open(ctx: Context, url: String, mime: String) {
        val c = (ctx.applicationContext as BuzzitApp).container
        c.scope.launch {
            val file = runCatching {
                val dir = File(ctx.cacheDir, "shared").apply { mkdirs() }
                val target = File(dir, url.substringAfterLast('/') + extension(mime))
                if (!target.exists()) {
                    c.api.http.newCall(Request.Builder().url(url).build()).await().use { resp ->
                        if (!resp.isSuccessful) error("download failed (${resp.code})")
                        withContext(Dispatchers.IO) { target.outputStream().use { out -> resp.body!!.byteStream().copyTo(out) } }
                    }
                }
                target
            }.getOrNull()
            withContext(Dispatchers.Main) {
                if (file == null) {
                    Toast.makeText(ctx, "Couldn't download the file", Toast.LENGTH_SHORT).show()
                    return@withContext
                }
                val uri = FileProvider.getUriForFile(ctx, ctx.packageName + ".files", file)
                val view = Intent(Intent.ACTION_VIEW).setDataAndType(uri, mime)
                    .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
                try {
                    ctx.startActivity(view)
                } catch (_: ActivityNotFoundException) {
                    Toast.makeText(ctx, "No app can open this file", Toast.LENGTH_SHORT).show()
                }
            }
        }
    }

    private fun extension(mime: String) = when {
        mime == "image/jpeg" -> ".jpg"
        mime.startsWith("image/") -> "." + mime.substringAfter('/')
        mime == "application/pdf" -> ".pdf"
        mime.startsWith("video/") -> ".mp4"
        mime.startsWith("audio/") -> ".m4a"
        else -> ""
    }
}
