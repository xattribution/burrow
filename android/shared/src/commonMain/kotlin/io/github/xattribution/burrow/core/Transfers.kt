package io.github.xattribution.burrow.core

import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import okio.Buffer
import okio.ByteString.Companion.toByteString
import okio.HashingSink
import okio.Source
import okio.blackholeSink
import okio.buffer

/** A file's size and modification time (milliseconds): how changes are noticed. */
data class Stamp(val size: Long, val mtime: Long)

/** The server refused to replace a file because it changed since it was last seen. */
class UploadConflictException(val conflict: Conflict) : Exception("It changed on Burrow in the meantime.")

/**
 * Resumable uploads: 8 MB chunks, each sent whole or not at all, a SHA-256 of
 * the whole file checked by the server at the end. An interrupted upload picks
 * up from the last chunk Burrow has.
 */
class Uploader(private val api: BurrowApi, private val chunkSize: Int = 8 shl 20) {
    suspend fun upload(
        dir: String,
        name: String,
        size: Long,
        mtime: Long,
        open: () -> Source,
        conflict: String = "rename",
        expect: Stamp? = null,
        resumeId: String? = null,
        saveId: suspend (String?) -> Unit = {},
        progress: (Long) -> Unit = {},
    ): UploadDone {
        var id = resumeId
        var offset = 0L
        if (id != null) {
            offset = try {
                api.uploadOffset(id)
            } catch (e: ApiException) {
                if (e.status != 404) throw e
                id = null
                0L
            }
        }
        if (id == null) {
            val st = api.startUpload(dir, name, size, mtime, conflict = conflict, expectSize = expect?.size, expectMtime = expect?.mtime)
            if (st.skipped) return UploadDone(path = st.path, name = name, size = st.size, mtime = st.mtime, skipped = true)
            id = st.id
            saveId(id)
        }
        var attempt = 0
        while (true) {
            val hasher = HashingSink.sha256(blackholeSink())
            val src = open().buffer()
            try {
                // hash what Burrow already has, then send the rest chunk by chunk
                var skip = offset
                val tmp = ByteArray(64 * 1024)
                while (skip > 0) {
                    val n = src.read(tmp, 0, minOf(skip, tmp.size.toLong()).toInt())
                    if (n < 0) throw IllegalStateException("$name got shorter while it was being uploaded.")
                    hasher.write(Buffer().write(tmp, 0, n), n.toLong())
                    skip -= n
                }
                progress(offset)
                val chunk = ByteArray(chunkSize)
                var resync = false
                while (offset < size) {
                    currentCoroutineContext().ensureActive()
                    val len = minOf(chunkSize.toLong(), size - offset).toInt()
                    var got = 0
                    while (got < len) {
                        val n = src.read(chunk, got, len - got)
                        if (n < 0) throw IllegalStateException("$name got shorter while it was being uploaded.")
                        got += n
                    }
                    val next = try {
                        api.putChunk(id!!, offset, len.toLong()) { it.write(chunk, 0, len) }
                    } catch (e: ApiException) {
                        val c = api.conflictOf(e)
                        if (c != null && c.offset >= 0) {
                            offset = c.offset // Burrow has a different amount: start over from there
                            resync = true
                            break
                        }
                        throw e
                    }
                    hasher.write(Buffer().write(chunk, 0, len), len.toLong())
                    offset = next
                    progress(offset)
                }
                if (resync) {
                    if (++attempt > 3) throw IllegalStateException("The upload of $name kept losing its place.")
                    continue
                }
                if (!src.exhausted()) throw IllegalStateException("$name grew while it was being uploaded.")
            } finally {
                src.close()
            }
            val sha = hasher.hash.hex()
            try {
                val done = api.finishUpload(id!!, sha)
                saveId(null)
                return done
            } catch (e: ApiException) {
                when (e.status) {
                    409 -> {
                        val c = api.conflictOf(e) ?: Conflict(error = e.message.orEmpty())
                        if (c.offset >= 0 && !c.conflict) { // not complete after all
                            offset = c.offset
                            if (++attempt > 3) throw e
                            continue
                        }
                        runCatching { api.cancelUpload(id!!) }
                        saveId(null)
                        throw UploadConflictException(c)
                    }
                    410, 422 -> saveId(null)
                }
                throw e
            }
        }
    }

    companion object {
        fun sha256(open: () -> Source): String {
            val h = HashingSink.sha256(blackholeSink())
            val b = h.buffer()
            open().buffer().use { b.writeAll(it) }
            b.flush()
            return h.hash.hex()
        }
    }
}

internal fun ByteArray.hex(): String = toByteString().hex()
