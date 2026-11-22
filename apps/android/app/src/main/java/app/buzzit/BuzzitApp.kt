package app.buzzit

import android.app.Application
import app.buzzit.buzz.BuzzAlertService
import app.buzzit.service.DeviceRegistration
import app.buzzit.service.Notifier
import coil.ImageLoader
import coil.ImageLoaderFactory
import coil.disk.DiskCache
import coil.memory.MemoryCache

class BuzzitApp : Application(), ImageLoaderFactory {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
        Notifier.createChannels(this)
        BuzzAlertService.ensureChannels(this)
        DeviceRegistration.initFirebaseIfConfigured(this)
    }

    /** Coil uses our authenticated OkHttp client so it can load private media. */
    override fun newImageLoader(): ImageLoader = ImageLoader.Builder(this)
        .okHttpClient(container.api.http)
        .memoryCache { MemoryCache.Builder(this).maxSizePercent(0.2).build() }
        .diskCache { DiskCache.Builder().directory(cacheDir.resolve("media")).maxSizeBytes(256L * 1024 * 1024).build() }
        .crossfade(true)
        .build()
}
