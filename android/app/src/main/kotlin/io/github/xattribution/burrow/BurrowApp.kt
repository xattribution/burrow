package io.github.xattribution.burrow

import android.app.Application
import android.content.Context
import coil3.ImageLoader
import coil3.PlatformContext
import coil3.SingletonImageLoader
import io.github.xattribution.burrow.data.KeyWrapper
import io.github.xattribution.burrow.data.KeystoreWrapper

open class BurrowApp : Application(), SingletonImageLoader.Factory {
    lateinit var graph: AppGraph
        private set

    override fun onCreate() {
        super.onCreate()
        graph = AppGraph(this, keyWrapper())
        graph.notifications.createChannels()
        graph.scheduler.reschedule()
        Thread { tidyCache() }.start()
    }

    /** Files opened in other apps are kept a day; copies waiting to upload, a week. */
    private fun tidyCache() {
        val now = System.currentTimeMillis()
        cacheDir.resolve("open").listFiles()?.filter { now - it.lastModified() > 86_400_000 }?.forEach { it.delete() }
        cacheDir.resolve("outbox").listFiles()?.filter { now - it.lastModified() > 7 * 86_400_000L }?.forEach { it.deleteRecursively() }
    }

    /** Where the device key is sealed: the Android Keystore (tests swap it out). */
    open fun keyWrapper(): KeyWrapper = KeystoreWrapper()

    override fun newImageLoader(context: PlatformContext): ImageLoader = graph.imageLoader()
}

val Context.graph: AppGraph get() = (applicationContext as BurrowApp).graph
