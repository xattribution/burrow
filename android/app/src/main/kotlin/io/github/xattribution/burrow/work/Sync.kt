package io.github.xattribution.burrow.work

import android.content.Context
import android.content.pm.ServiceInfo
import android.provider.MediaStore
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import io.github.xattribution.burrow.core.ApiException
import io.github.xattribution.burrow.core.ChannelException
import io.github.xattribution.burrow.core.NotPairedException
import io.github.xattribution.burrow.core.SyncEngine
import io.github.xattribution.burrow.core.UnreachableException
import io.github.xattribution.burrow.core.db.BurrowDb
import io.github.xattribution.burrow.data.MediaTree
import io.github.xattribution.burrow.data.localTreeFor
import io.github.xattribution.burrow.graph
import kotlinx.coroutines.flow.MutableStateFlow
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.TimeUnit

/** What a pair is doing right now, for the Sync screen. */
data class LiveSync(val pairId: Long, val phase: String, val done: Int, val total: Int, val bytes: Long, val totalBytes: Long, val current: String)

/**
 * Schedules each pair on its own: hourly with its own conditions (Wi-Fi only,
 * charging only), plus a run shortly after new photos appear for photo backups.
 */
class SyncScheduler(private val context: Context, private val db: () -> BurrowDb) {
    private val wm get() = WorkManager.getInstance(context)
    val live = MutableStateFlow<LiveSync?>(null)

    private fun constraints(wifiOnly: Boolean, charging: Boolean) = Constraints.Builder()
        .setRequiredNetworkType(if (wifiOnly) NetworkType.UNMETERED else NetworkType.CONNECTED)
        .setRequiresCharging(charging)
        .setRequiresBatteryNotLow(true)
        .build()

    fun reschedule() {
        val pairs = runCatching { db().pairQueries.all().executeAsList() }.getOrDefault(emptyList())
        for (p in pairs) {
            val name = "sync-${p.id}"
            if (p.enabled == 0L) {
                wm.cancelUniqueWork(name)
                continue
            }
            val req = PeriodicWorkRequestBuilder<SyncWorker>(1, TimeUnit.HOURS)
                .setConstraints(constraints(p.wifi_only != 0L, p.charging_only != 0L))
                .setInputData(workDataOf(SyncWorker.PAIR to p.id))
                .addTag(TAG)
                .build()
            wm.enqueueUniquePeriodicWork(name, ExistingPeriodicWorkPolicy.UPDATE, req)
        }
        if (pairs.any { it.enabled != 0L && it.local_uri.startsWith(MediaTree.PREFIX) }) watchPhotos() else wm.cancelUniqueWork(PHOTOS)
    }

    /** New photos or videos start a photo backup within a couple of minutes. */
    fun watchPhotos() {
        val c = Constraints.Builder()
            .addContentUriTrigger(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, true)
            .addContentUriTrigger(MediaStore.Video.Media.EXTERNAL_CONTENT_URI, true)
            .setTriggerContentUpdateDelay(30, TimeUnit.SECONDS)
            .setTriggerContentMaxDelay(2, TimeUnit.MINUTES)
            .setRequiredNetworkType(NetworkType.CONNECTED)
            .build()
        wm.enqueueUniqueWork(PHOTOS, ExistingWorkPolicy.REPLACE, OneTimeWorkRequestBuilder<SyncWorker>().setConstraints(c).setInputData(workDataOf(SyncWorker.PHOTOS_ONLY to true)).build())
    }

    /** Syncs one pair (or all) now, honoring only the need for a network. */
    fun syncNow(pairId: Long? = null) {
        val req = OneTimeWorkRequestBuilder<SyncWorker>()
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
            .setInputData(if (pairId != null) workDataOf(SyncWorker.PAIR to pairId, SyncWorker.NOW to true) else workDataOf(SyncWorker.NOW to true))
            .build()
        wm.enqueueUniqueWork("sync-now-${pairId ?: "all"}", ExistingWorkPolicy.KEEP, req)
    }

    fun cancel(pairId: Long) {
        wm.cancelUniqueWork("sync-$pairId")
        wm.cancelUniqueWork("sync-now-$pairId")
    }

    companion object {
        const val TAG = "burrow-sync"
        const val PHOTOS = "photos-watch"
    }
}

class SyncWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    private val g = context.graph

    override suspend fun getForegroundInfo(): ForegroundInfo = foreground("Syncing with Burrow", "", 0, 0)

    private fun foreground(title: String, text: String, done: Long, total: Long) =
        ForegroundInfo(Notifications.ID_SYNC, g.notifications.progress(Notifications.SYNC, title, text, done, total), ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)

    override suspend fun doWork(): Result {
        val api = g.connection.api() ?: return Result.success() // not paired: nothing to do
        val photosOnly = inputData.getBoolean(PHOTOS_ONLY, false)
        val now = inputData.getBoolean(NOW, false)
        val one = inputData.getLong(PAIR, -1)
        val q = g.db.pairQueries
        val pairs = q.all().executeAsList().filter { p ->
            p.enabled != 0L && (one < 0 || p.id == one) && (!photosOnly || p.local_uri.startsWith(MediaTree.PREFIX))
        }
        val fmt = SimpleDateFormat("yyyy-MM-dd HHmm", Locale.US)
        val device = g.device.current?.info?.deviceName?.ifBlank { null } ?: android.os.Build.MODEL
        val engine = SyncEngine(api, g.db, conflictTag = { "conflict $device ${fmt.format(Date())}" })
        var retry = false
        try {
            for (p in pairs) {
                // a scheduled run keeps to the pair's conditions even if the system starts it early
                if (!now && p.wifi_only != 0L && !g.connection.routes.unmetered()) continue
                runCatching { setForeground(foreground("Syncing ${p.name}", "Checking", 0, 0)) }
                val tree = localTreeFor(applicationContext, p.local_uri)
                try {
                    val rep = g.connection.use { engine.sync(p, tree) { pr ->
                        g.scheduler.live.value = LiveSync(p.id, pr.phase, pr.done, pr.total, pr.bytes, pr.totalBytes, pr.current)
                        val text = if (pr.total > 0) "${pr.phase} ${pr.done + 1} of ${pr.total}: ${pr.current.substringAfterLast('/')}" else pr.phase
                        runCatching { setForegroundAsync(foreground("Syncing ${p.name}", text, pr.bytes, pr.totalBytes)) }
                    } }
                    if (rep.errors.isNotEmpty()) {
                        g.notifications.alert(Notifications.ID_SYNC_PROBLEM, "Some files in ${p.name} didn't sync", rep.errors.take(3).joinToString("\n"))
                    }
                } catch (e: SecurityException) {
                    // the folder permission was taken back (or the photo permission): the pair can't run
                    q.finished(p.last_sync, "Burrow lost access to this folder on the phone. Pick it again.", p.id)
                } catch (e: ApiException) {
                    // Burrow refused this pair (its folder is gone, say): the engine noted why on the pair; go on with the others
                }
            }
        } catch (e: NotPairedException) {
            g.notifications.alert(Notifications.ID_REMOVED, "This phone was removed from Burrow", "Pair it again to keep syncing.")
            return Result.failure()
        } catch (e: UnreachableException) {
            retry = true
        } catch (e: ChannelException) {
            retry = true
        } catch (e: java.io.IOException) {
            retry = true
        } finally {
            g.scheduler.live.value = null
        }
        if (photosOnly) g.scheduler.watchPhotos() // a content trigger fires once; set it again
        return if (retry && runAttemptCount < 5) Result.retry() else Result.success()
    }

    companion object {
        const val PAIR = "pair"
        const val NOW = "now"
        const val PHOTOS_ONLY = "photos"
    }
}
