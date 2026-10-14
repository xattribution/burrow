package io.github.xattribution.burrow.data

import com.hierynomus.msdtyp.AccessMask
import com.hierynomus.msdtyp.FileTime
import com.hierynomus.mserref.NtStatus
import com.hierynomus.msfscc.FileAttributes
import com.hierynomus.msfscc.fileinformation.FileBasicInformation
import com.hierynomus.mssmb2.SMB2CreateDisposition
import com.hierynomus.mssmb2.SMB2CreateOptions
import com.hierynomus.mssmb2.SMB2ShareAccess
import com.hierynomus.mssmb2.SMBApiException
import com.hierynomus.security.bc.BCSecurityProvider
import com.hierynomus.smbj.SMBClient
import com.hierynomus.smbj.SmbConfig
import com.hierynomus.smbj.auth.AuthenticationContext
import com.hierynomus.smbj.share.DiskShare
import io.github.xattribution.burrow.core.ApiException
import io.github.xattribution.burrow.core.Entry
import io.github.xattribution.burrow.core.Listing
import io.github.xattribution.burrow.core.Uploader
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import okio.BufferedSource
import okio.Source
import okio.buffer
import okio.source
import java.io.IOException
import java.io.InputStream
import java.util.EnumSet
import java.util.concurrent.TimeUnit

/**
 * One place the app can browse: the Burrow this phone is paired with, another
 * Burrow, or an SMB share. Paths are inside the place ("" is its top).
 */
interface PlaceFs {
    val id: String

    /** Burrow places have links, favorites, search and server thumbnails; an SMB share has files. */
    val isBurrow: Boolean

    suspend fun ls(path: String): Listing
    suspend fun stat(path: String): Entry

    /** Reads the file from [from] on. Close what comes back. */
    suspend fun open(path: String, from: Long = 0): Opened

    /** Writes a new file into [dir], renaming it "name (2).ext" if the name is taken; returns its path. */
    suspend fun put(dir: String, name: String, size: Long, mtimeMs: Long, open: () -> Source, progress: (Long) -> Unit = {}): String
    suspend fun mkdir(path: String)

    /** Moves or renames, never over something already there; returns the new path. */
    suspend fun move(from: String, to: String): String
    suspend fun delete(path: String)
    fun close() {}
}

class Opened(val size: Long, val source: BufferedSource, private val release: () -> Unit) : AutoCloseable {
    override fun close() = release()
}

/** A Burrow, through its encrypted channel. */
class BurrowFs(override val id: String, val conn: Connection) : PlaceFs {
    override val isBurrow = true

    override suspend fun ls(path: String) = conn.use { it.ls(path) }
    override suspend fun stat(path: String) = conn.use { it.info(path).entry }

    override suspend fun open(path: String, from: Long): Opened {
        val r = conn.use { it.raw(path, from) }
        // a server that ignored the range sent the whole file
        if (from > 0 && r.status != 206) r.body.skip(from)
        // "bytes 100-999/1000" when a range was asked for, else the length
        val total = r.header("Content-Range")?.substringAfterLast('/')?.toLongOrNull()
            ?: r.header("Content-Length")?.toLongOrNull()?.plus(if (r.status == 206) from else 0) ?: -1
        return Opened(total, r.body, r::close)
    }

    override suspend fun put(dir: String, name: String, size: Long, mtimeMs: Long, open: () -> Source, progress: (Long) -> Unit) =
        conn.use { Uploader(it).upload(dir = dir, name = name, size = size, mtime = mtimeMs, open = open, conflict = "rename", progress = progress).path }

    override suspend fun mkdir(path: String) {
        conn.use { it.mkdir(path) }
    }

    override suspend fun move(from: String, to: String) = conn.use { it.move(from, to) }

    override suspend fun delete(path: String) {
        val r = conn.use { it.delete(listOf(path)) }.first()
        if (!r.ok) throw ApiException(r.code, r.error.ifEmpty { "Couldn't delete it." })
    }
}

@Serializable
data class SmbSettings(
    val host: String,
    val port: Int = 445,
    val share: String,
    val user: String = "",
    val password: String = "",
    val domain: String = "",
    val folder: String = "",
)

/**
 * An SMB share (Windows, Synology, TrueNAS, Unraid), straight from the phone.
 * One connection is kept and opened again after it drops; deletes are
 * permanent, since a share has no trash the app can show.
 */
class SmbFs(override val id: String, private val s: SmbSettings) : PlaceFs {
    override val isBurrow = false
    private val lock = Mutex()
    private var client: SMBClient? = null
    private var share: DiskShare? = null

    private suspend fun disk(fresh: Boolean = false): DiskShare = lock.withLock {
        share?.takeIf { !fresh && it.isConnected }?.let { return it }
        runCatching { client?.close() }
        val cfg = SmbConfig.builder().withSecurityProvider(BCSecurityProvider()).withTimeout(20, TimeUnit.SECONDS).withSoTimeout(30, TimeUnit.SECONDS).build()
        val c = SMBClient(cfg)
        client = c
        try {
            val conn = c.connect(s.host, s.port)
            val session = conn.authenticate(AuthenticationContext(s.user, s.password.toCharArray(), s.domain.ifBlank { null }))
            (session.connectShare(s.share) as? DiskShare ?: throw IOException("${s.share} isn't a folder share")).also { share = it }
        } catch (e: SMBApiException) {
            throw IOException(smbMessage(e), e)
        }
    }

    /** Runs [block] on the share, once more on a fresh connection if the old one dropped. */
    private suspend fun <T> withShare(block: (DiskShare) -> T): T = withContext(Dispatchers.IO) {
        try {
            block(disk())
        } catch (e: SMBApiException) {
            throw IOException(smbMessage(e), e)
        } catch (e: Exception) {
            if (e is IOException && e.cause is SMBApiException) throw e
            try {
                block(disk(fresh = true))
            } catch (e2: SMBApiException) {
                throw IOException(smbMessage(e2), e2)
            }
        }
    }

    private fun full(p: String): String = listOf(s.folder.trim('/', '\\'), p.trim('/')).filter { it.isNotEmpty() }.joinToString("/").replace('/', '\\')

    override suspend fun ls(path: String): Listing = withShare { d ->
        val out = d.list(full(path)).filter { f ->
            val n = f.fileName
            val hidden = f.fileAttributes and (FileAttributes.FILE_ATTRIBUTE_HIDDEN.value or FileAttributes.FILE_ATTRIBUTE_SYSTEM.value) != 0L
            n != "." && n != ".." && !hidden && !n.startsWith(".burrow-") && !n.startsWith("~$")
        }.map { f ->
            val dir = f.fileAttributes and FileAttributes.FILE_ATTRIBUTE_DIRECTORY.value != 0L
            entryOf(join(path, f.fileName), f.fileName, dir, f.endOfFile, f.lastWriteTime.toEpochMillis() / 1000)
        }.sortedWith(compareBy<Entry>({ !it.dir }, { it.name.lowercase() }))
        Listing(path = path, entries = out, canShare = false, canWrite = true)
    }

    override suspend fun stat(path: String): Entry = withShare { d ->
        val i = d.getFileInformation(full(path))
        entryOf(path, path.substringAfterLast('/').ifEmpty { s.share }, i.standardInformation.isDirectory, i.standardInformation.endOfFile, i.basicInformation.lastWriteTime.toEpochMillis() / 1000)
    }

    override suspend fun open(path: String, from: Long): Opened = withShare { d ->
        val f = d.openFile(full(path), EnumSet.of(AccessMask.GENERIC_READ), null, SMB2ShareAccess.ALL, SMB2CreateDisposition.FILE_OPEN, null)
        val size = f.fileInformation.standardInformation.endOfFile
        // reads at an offset, so a player that seeks doesn't read through the file
        val input = object : InputStream() {
            var pos = from
            override fun read(): Int {
                val b = ByteArray(1)
                return if (read(b, 0, 1) <= 0) -1 else b[0].toInt() and 0xff
            }

            override fun read(b: ByteArray, off: Int, len: Int): Int {
                if (pos >= size) return -1
                val n = f.read(b, pos, off, minOf(len.toLong(), size - pos).toInt())
                if (n <= 0) return -1
                pos += n
                return n
            }

            override fun close() = f.close()
        }
        Opened(size, input.source().buffer(), input::close)
    }

    override suspend fun put(dir: String, name: String, size: Long, mtimeMs: Long, open: () -> Source, progress: (Long) -> Unit): String {
        val ext = name.substringAfterLast('.', "").let { if (it.isEmpty() || it == name) "" else ".$it" }
        val base = name.removeSuffix(ext)
        for (i in 1 until 1000) {
            val cand = if (i == 1) name else "$base ($i)$ext"
            val p = join(dir, cand)
            val made = withShare { d ->
                try {
                    d.openFile(full(p), EnumSet.of(AccessMask.GENERIC_WRITE, AccessMask.FILE_WRITE_ATTRIBUTES, AccessMask.DELETE), EnumSet.of(FileAttributes.FILE_ATTRIBUTE_NORMAL), SMB2ShareAccess.ALL, SMB2CreateDisposition.FILE_CREATE, EnumSet.of(SMB2CreateOptions.FILE_NON_DIRECTORY_FILE))
                } catch (e: SMBApiException) {
                    if (e.status == NtStatus.STATUS_OBJECT_NAME_COLLISION) null else throw e
                }
            } ?: continue
            withContext(Dispatchers.IO) {
                var ok = false
                try {
                    made.outputStream.use { out ->
                        open().buffer().use { src ->
                            val buf = ByteArray(256 * 1024)
                            var sent = 0L
                            while (true) {
                                val n = src.read(buf)
                                if (n < 0) break
                                out.write(buf, 0, n)
                                sent += n
                                progress(sent)
                            }
                            if (size >= 0 && sent != size) throw IOException("The file changed while it was being sent.")
                        }
                    }
                    if (mtimeMs > 0) {
                        val t = FileTime.ofEpochMillis(mtimeMs)
                        runCatching { made.setFileInformation(FileBasicInformation(t, t, t, t, FileAttributes.FILE_ATTRIBUTE_NORMAL.value)) }
                    }
                    ok = true
                } finally {
                    if (!ok) runCatching { made.deleteOnClose() }
                    made.close()
                }
            }
            return p
        }
        throw IOException("Too many files with that name.")
    }

    override suspend fun mkdir(path: String) = withShare { it.mkdir(full(path)) }

    override suspend fun move(from: String, to: String): String = withShare { d ->
        val e = d.open(full(from), EnumSet.of(AccessMask.DELETE, AccessMask.GENERIC_READ), null, SMB2ShareAccess.ALL, SMB2CreateDisposition.FILE_OPEN, null)
        e.use { it.rename(full(to), false) }
        to
    }

    override suspend fun delete(path: String) = withShare { d ->
        if (d.getFileInformation(full(path)).standardInformation.isDirectory) d.rmdir(full(path), true) else d.rm(full(path))
    }

    override fun close() {
        runCatching { client?.close() }
        client = null
        share = null
    }

    companion object {
        /** Tries the settings: signs in, opens the share and lists its top. */
        suspend fun check(s: SmbSettings) {
            val fs = SmbFs("check", s)
            try {
                fs.ls("")
            } finally {
                fs.close()
            }
        }

        fun smbMessage(e: SMBApiException): String = when (e.status) {
            NtStatus.STATUS_LOGON_FAILURE, NtStatus.STATUS_LOGON_TYPE_NOT_GRANTED -> "The NAS turned down that user name and password."
            NtStatus.STATUS_ACCESS_DENIED -> "The NAS says this account can't do that."
            NtStatus.STATUS_OBJECT_NAME_NOT_FOUND, NtStatus.STATUS_OBJECT_PATH_NOT_FOUND, NtStatus.STATUS_NOT_FOUND -> "It isn't there any more."
            NtStatus.STATUS_OBJECT_NAME_COLLISION -> "Something with that name is already there."
            NtStatus.STATUS_BAD_NETWORK_NAME -> "The NAS has no share with that name."
            else -> "The NAS said ${e.status}."
        }
    }
}

private fun join(dir: String, name: String) = if (dir.isEmpty()) name else "$dir/$name"

/** An entry from outside Burrow, with the kind Burrow would give it. */
internal fun entryOf(path: String, name: String, dir: Boolean, size: Long, mtime: Long): Entry {
    val ext = if (dir) "" else name.substringAfterLast('.', "").lowercase().takeIf { it != name.lowercase() }.orEmpty()
    return Entry(path = path, name = name, dir = dir, size = if (dir) 0 else size, mtime = mtime, kind = if (dir) "folder" else kindOfExt[ext] ?: "", ext = ext)
}

private val kindOfExt: Map<String, String> = buildMap {
    listOf("jpg", "jpeg", "png", "gif", "webp", "heic", "heif", "bmp", "tif", "tiff", "avif").forEach { put(it, "image") }
    listOf("mp4", "mov", "mkv", "webm", "avi", "m4v", "3gp", "ts").forEach { put(it, "video") }
    listOf("mp3", "flac", "wav", "m4a", "ogg", "opus", "aac", "wma").forEach { put(it, "audio") }
    listOf("pdf", "doc", "docx", "txt", "md", "odt", "rtf", "xls", "xlsx", "ppt", "pptx", "csv", "pages", "key", "numbers").forEach { put(it, "doc") }
    listOf("zip", "7z", "rar", "tar", "gz", "bz2", "xz", "zst").forEach { put(it, "archive") }
    listOf("kt", "java", "go", "rs", "py", "js", "ts", "c", "cpp", "h", "sh", "json", "yaml", "yml", "toml", "html", "css").forEach { put(it, "code") }
    listOf("stl", "3mf", "obj", "step", "stp", "f3d", "gcode", "ply").forEach { put(it, "3d") }
}
