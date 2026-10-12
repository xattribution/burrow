package io.github.xattribution.burrow

import android.app.Application
import app.cash.sqldelight.driver.android.AndroidSqliteDriver
import coil3.ImageLoader
import coil3.disk.DiskCache
import coil3.memory.MemoryCache
import coil3.request.crossfade
import io.github.xattribution.burrow.core.db.BurrowDb
import io.github.xattribution.burrow.data.Connection
import io.github.xattribution.burrow.data.DeviceStore
import io.github.xattribution.burrow.data.KeyWrapper
import io.github.xattribution.burrow.media.FileImageFetcher
import io.github.xattribution.burrow.media.ThumbFetcher
import io.github.xattribution.burrow.work.Notifications
import io.github.xattribution.burrow.work.SyncScheduler
import io.github.xattribution.burrow.work.Transfers
import okio.Path.Companion.toOkioPath

/** The app's long-lived pieces, made once per process. */
class AppGraph(val app: Application, keyWrapper: KeyWrapper) {
    val db: BurrowDb by lazy { BurrowDb(AndroidSqliteDriver(BurrowDb.Schema, app, "burrow.db")) }
    val device = DeviceStore(app, keyWrapper)
    val connection = Connection(app, device)
    val notifications = Notifications(app)
    val scheduler = SyncScheduler(app) { db }
    val transfers = Transfers(app) { db }

    fun imageLoader(): ImageLoader = ImageLoader.Builder(app)
        .components {
            add(ThumbFetcher.Factory(connection), ThumbFetcher.Thumb::class)
            add(ThumbFetcher.ThumbKeyer())
            add(FileImageFetcher.Factory(connection), FileImageFetcher.Image::class)
            add(FileImageFetcher.ImageKeyer())
        }
        .memoryCache { MemoryCache.Builder().maxSizePercent(app, 0.2).build() }
        .diskCache { DiskCache.Builder().directory(app.cacheDir.resolve("images").toOkioPath()).maxSizeBytes(256L shl 20).build() }
        .crossfade(true)
        .build()
}
