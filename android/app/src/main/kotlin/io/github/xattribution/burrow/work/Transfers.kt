package io.github.xattribution.burrow.work

import android.content.ContentValues
import android.content.Context
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Environment
import android.provider.MediaStore
import android.provider.OpenableColumns
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import io.github.xattribution.burrow.core.ApiException
import io.github.xattribution.burrow.core.NotPairedException
import io.github.xattribution.burrow.core.Uploader
import io.github.xattribution.burrow.core.db.BurrowDb
import io.github.xattribution.burrow.core.Entry
import io.github.xattribution.burrow.data.Place
import io.github.xattribution.burrow.data.PlaceFs
import io.github.xattribution.burrow.graph
import kotlinx.coroutines.runBlocking
import okio.ForwardingSource
import okio.Source
import kotlinx.coroutines.flow.MutableStateFlow
import okio.buffer
import okio.sink
import okio.source
import java.io.File
import java.io.FileNotFoundException

/** A one-off upload or download, queued and run in the background. */
class Transfers(private val context: Context, private val db: () -> BurrowDb) {
    /** Bumped whenever the queue changes, so screens can refresh. */
    val changed = MutableStateFlow(0L)

    fun upload(uri: Uri, dir: String, name: String? = null, place: String = Place.HOME) {
        val remoteDir = placed(place, dir)
        val cr = context.contentResolver
        var size = -1L
        var display = name
        if (uri.scheme == "file") {
            val f = java.io.File(uri.path!!)
            val now = System.currentTimeMillis() / 1000
            db().transferQueries.queue("up", uri.toString(), remoteDir, display ?: f.name, f.length(), f.lastModified(), now, now)
            kick()
            return
        }
        cr.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                if (display == null) display = c.getString(0)
                if (!c.isNull(1)) size = c.getLong(1)
            }
        }
        val mtime = runCatching {
            cr.query(uri, arrayOf("last_modified"), null, null, null)?.use { c -> if (c.moveToFirst() && !c.isNull(0)) c.getLong(0) else null }
        }.getOrNull() ?: System.currentTimeMillis()
        val now = System.currentTimeMillis() / 1000
        db().transferQueries.queue("up", uri.toString(), remoteDir, display ?: uri.lastPathSegment ?: "file", size, mtime, now, now)
        kick()
    }

    fun download(remotePath: String, size: Long, place: String = Place.HOME) {
        val now = System.currentTimeMillis() / 1000
        db().transferQueries.queue("down", "", placed(place, remotePath.substringBeforeLast('/', "")), remotePath.substringAfterLast('/'), size, 0, now, now)
        kick()
    }

    /**
     * Copies (or moves) a file or folder from one place to another through the
     * phone: read from one, written to the other, the original deleted last.
     */
    fun copy(from: String, e: Entry, to: String, dir: String, move: Boolean) {
        val now = System.currentTimeMillis() / 1000
        db().transferQueries.queue(if (move) "move" else "copy", placed(from, e.path), placed(to, dir), e.name, if (e.dir) -1 else e.size, e.mtime * 1000, now, now)
        kick()
    }

    fun kick() {
        changed.value++
        WorkManager.getInstance(context).enqueueUniqueWork(
            "transfers", ExistingWorkPolicy.APPEND_OR_REPLACE,
            OneTimeWorkRequestBuilder<TransferWorker>().setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()).build(),
        )
    }
}

class TransferWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    private val g = context.graph

    override suspend fun getForegroundInfo() = info("Sending to Burrow", "", 0, 0)

    private fun info(title: String, text: String, done: Long, total: Long) =
        ForegroundInfo(Notifications.ID_TRANSFER, g.notifications.progress(Notifications.TRANSFERS, title, text, done, total), ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)

    override suspend fun doWork(): Result {
        val api = g.connection.api() ?: return Result.success()
        val q = g.db.transferQueries
        while (true) {
            val t = q.next().executeAsOneOrNull() ?: break
            val now = { System.currentTimeMillis() / 1000 }
            try {
                val (place, dir) = unplace(t.remote_dir)
                if (t.direction == "copy" || t.direction == "move") {
                    copyAcross(t.id, t.name, t.local_uri, t.remote_dir, t.direction == "move")
                    q.finish("done", "${g.places.get(place)?.name ?: "Burrow"}/" + listOf(dir, t.name).filter { it.isNotEmpty() }.joinToString("/"), t.done_bytes, now(), t.id)
                } else if (t.direction == "up" && place != Place.HOME) {
                    val uri = Uri.parse(t.local_uri)
                    var size = t.size
                    if (size < 0) size = applicationContext.contentResolver.openInputStream(uri)?.use { it.skip(Long.MAX_VALUE) } ?: throw FileNotFoundException(t.name)
                    runCatching { setForeground(info("Sending ${t.name}", g.places.get(place)?.name.orEmpty(), 0, size)) }
                    val open = { if (uri.scheme == "file") File(uri.path!!).source() else (applicationContext.contentResolver.openInputStream(uri) ?: throw FileNotFoundException(t.name)).source() }
                    val path = g.places.fs(place).put(dir, t.name, size, t.mtime, open) { sent ->
                        q.progress(sent, "", now(), t.id)
                        g.transfers.changed.value++
                    }
                    q.finish("done", path, size, now(), t.id)
                    if (uri.scheme == "file" && uri.path?.startsWith(applicationContext.cacheDir.path) == true) File(uri.path!!).parentFile?.deleteRecursively()
                } else if (t.direction == "up") {
                    val uri = Uri.parse(t.local_uri)
                    var size = t.size
                    if (size < 0) size = applicationContext.contentResolver.openInputStream(uri)?.use { it.skip(Long.MAX_VALUE) } ?: throw FileNotFoundException(t.name)
                    runCatching { setForeground(info("Sending ${t.name}", t.remote_dir, 0, size)) }
                    var uploadId = t.upload_id
                    val done = g.connection.use {
                        Uploader(api).upload(
                            dir = t.remote_dir, name = t.name, size = size, mtime = t.mtime,
                            open = { if (uri.scheme == "file") File(uri.path!!).source() else (applicationContext.contentResolver.openInputStream(uri) ?: throw FileNotFoundException(t.name)).source() },
                            conflict = "rename", resumeId = t.upload_id.ifEmpty { null },
                            saveId = { id -> uploadId = id.orEmpty(); q.progress(t.done_bytes, uploadId, now(), t.id) },
                            progress = { sent ->
                                q.progress(sent, uploadId, now(), t.id)
                                g.transfers.changed.value++
                                setForegroundAsync(info("Sending ${t.name}", t.remote_dir, sent, size))
                            },
                        )
                    }
                    q.finish("done", done.path, size, now(), t.id)
                    // a copy made from a share sheet isn't needed any more
                    if (uri.scheme == "file" && uri.path?.startsWith(applicationContext.cacheDir.path) == true) File(uri.path!!).parentFile?.deleteRecursively()
                } else {
                    runCatching { setForeground(info("Saving ${t.name}", "to Downloads/Burrow", 0, t.size)) }
                    val saved = saveToDownloads(g.places.fs(place), dir, t.name, t.size) { got ->
                        q.progress(got, "", now(), t.id)
                        g.transfers.changed.value++
                        setForegroundAsync(info("Saving ${t.name}", "to Downloads/Burrow", got, t.size))
                    }
                    q.finish("done", saved, t.size, now(), t.id)
                }
            } catch (e: NotPairedException) {
                q.finish("failed", "This phone was removed from Burrow.", t.done_bytes, now(), t.id)
                return Result.failure()
            } catch (e: ApiException) {
                q.finish("failed", e.message.orEmpty(), t.done_bytes, now(), t.id)
            } catch (e: FileNotFoundException) {
                q.finish("failed", "The file on the phone is gone.", t.done_bytes, now(), t.id)
            } catch (e: SecurityException) {
                q.finish("failed", "Burrow can't read that file any more.", t.done_bytes, now(), t.id)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                // network trouble: leave it queued and try again later
                g.transfers.changed.value++
                return if (runAttemptCount < 8) Result.retry() else Result.failure()
            }
            g.transfers.changed.value++
        }
        return Result.success()
    }

    /** Reads from one place and writes into another; folders are copied whole. */
    private suspend fun copyAcross(id: Long, name: String, from: String, to: String, move: Boolean) {
        val (sp, spath) = unplace(from)
        val (dp, ddir) = unplace(to)
        val src = g.places.fs(sp)
        val dst = g.places.fs(dp)
        val q = g.db.transferQueries
        val label = "${if (move) "Moving" else "Copying"} $name"
        val where = "to ${g.places.get(dp)?.name ?: "Burrow"}"
        var done = 0L
        suspend fun copy(e: Entry, into: String) {
            if (e.dir) {
                val target = if (into.isEmpty()) e.name else "$into/${e.name}"
                try {
                    dst.mkdir(target)
                } catch (x: Exception) {
                    // already there: the copy merges into it
                    if (runCatching { dst.stat(target).dir }.getOrDefault(false).not()) throw x
                }
                for (c in src.ls(e.path).entries) copy(c, target)
                return
            }
            runCatching { setForeground(info(label, where, done, done + e.size)) }
            dst.put(into, e.name, e.size, e.mtime * 1000, open = { openSource(src, e.path) }) { n ->
                q.progress(done + n, "", System.currentTimeMillis() / 1000, id)
                g.transfers.changed.value++
            }
            done += e.size
        }
        copy(src.stat(spath).copy(path = spath), ddir)
        if (move) src.delete(spath)
    }

    private fun openSource(fs: PlaceFs, path: String): Source {
        val o = runBlocking { fs.open(path) }
        return object : ForwardingSource(o.source) {
            override fun close() {
                super.close()
                o.close()
            }
        }
    }

    /** Streams a file into Downloads/Burrow through the media store. */
    private suspend fun saveToDownloads(fs: PlaceFs, dir: String, name: String, size: Long, progress: (Long) -> Unit): String {
        val cr = applicationContext.contentResolver
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, name)
            put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/Burrow")
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        val uri = cr.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) ?: throw FileNotFoundException("Downloads isn't available")
        try {
            fs.open(if (dir.isEmpty()) name else "$dir/$name").use { r ->
                (cr.openOutputStream(uri, "wt") ?: throw FileNotFoundException(name)).sink().buffer().use { out ->
                    var got = 0L
                    val buf = okio.Buffer()
                    while (true) {
                        val n = r.source.read(buf, 256 * 1024)
                        if (n < 0) break
                        out.write(buf, n)
                        got += n
                        progress(got)
                    }
                }
            }
            cr.update(uri, ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }, null, null)
            return "Downloads/Burrow/$name"
        } catch (e: Exception) {
            cr.delete(uri, null, null)
            throw e
        }
    }
}

/** A folder in a place, as one queue column: this phone's Burrow stays a plain path. */
internal fun placed(place: String, path: String) = if (place == Place.HOME) path else "$place\u0000$path"

internal fun unplace(s: String): Pair<String, String> {
    val i = s.indexOf('\u0000')
    return if (i < 0) Place.HOME to s else s.substring(0, i) to s.substring(i + 1)
}
