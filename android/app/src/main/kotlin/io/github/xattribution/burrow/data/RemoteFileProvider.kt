package io.github.xattribution.burrow.data

import android.content.ContentProvider
import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.os.Handler
import android.os.HandlerThread
import android.os.ParcelFileDescriptor
import android.os.ProxyFileDescriptorCallback
import android.os.storage.StorageManager
import android.provider.OpenableColumns
import android.system.ErrnoException
import android.system.OsConstants
import android.webkit.MimeTypeMap
import io.github.xattribution.burrow.core.Entry
import io.github.xattribution.burrow.graph
import kotlinx.coroutines.runBlocking
import java.io.FileNotFoundException

/**
 * NAS files as content:// addresses, so a file dragged out of Burrow lands in
 * another app without being downloaded first. Reading the address streams the
 * file from Burrow, and the other app can seek in it (a video player, a zip
 * viewer). The provider isn't exported: an app can only read an address while it
 * holds the grant that came with the drag.
 */
class RemoteFileProvider : ContentProvider() {
    private val io by lazy { Handler(HandlerThread("remote-files").apply { start() }.looper) }

    override fun onCreate() = true

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, args: Array<out String>?, sort: String?): Cursor {
        val f = parse(uri) ?: throw FileNotFoundException(uri.toString())
        val cols = projection ?: arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE)
        return MatrixCursor(cols, 1).apply {
            addRow(cols.map { when (it) { OpenableColumns.DISPLAY_NAME -> f.name; OpenableColumns.SIZE -> f.size; else -> null } })
        }
    }

    override fun getType(uri: Uri): String? = parse(uri)?.let { mimeOf(it.name) }

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        if (mode != "r") throw SecurityException("Files from Burrow are read-only here.")
        val f = parse(uri) ?: throw FileNotFoundException(uri.toString())
        val ctx = context ?: throw FileNotFoundException(uri.toString())
        val sm = ctx.getSystemService(StorageManager::class.java)
        return sm.openProxyFileDescriptor(ParcelFileDescriptor.MODE_READ_ONLY, Reader(ctx.graph.places.fs(f.place), f), io)
    }

    override fun insert(uri: Uri, values: ContentValues?): Uri? = throw UnsupportedOperationException()
    override fun delete(uri: Uri, selection: String?, args: Array<out String>?): Int = throw UnsupportedOperationException()
    override fun update(uri: Uri, values: ContentValues?, selection: String?, args: Array<out String>?): Int = throw UnsupportedOperationException()

    internal data class Remote(val place: String, val path: String, val size: Long) {
        val name get() = path.substringAfterLast('/')
    }

    /**
     * Reads ahead on one open stream while the other app reads in order, and
     * reopens at the new offset when it jumps.
     */
    internal class Reader(private val fs: PlaceFs, private val f: Remote) : ProxyFileDescriptorCallback() {
        private var open: Opened? = null
        private var pos = -1L

        override fun onGetSize() = f.size

        override fun onRead(offset: Long, size: Int, data: ByteArray): Int {
            if (offset >= f.size) return 0
            try {
                if (open == null || offset != pos) {
                    open?.close()
                    open = runBlocking { fs.open(f.path, from = offset) }
                    pos = offset
                }
                val src = open!!.source
                var n = 0
                while (n < size) {
                    val got = src.read(data, n, size - n)
                    if (got < 0) break
                    n += got
                }
                pos += n
                return n
            } catch (e: Exception) {
                open?.close()
                open = null
                throw ErrnoException("read", OsConstants.EIO)
            }
        }

        override fun onRelease() {
            open?.close()
            open = null
        }
    }

    companion object {
        fun authority(ctx: Context) = ctx.packageName + ".remote"

        /** The address another app reads [e] at. */
        fun uriFor(ctx: Context, e: Entry, place: String = Place.HOME): Uri = Uri.Builder().scheme("content").authority(authority(ctx))
            .appendPath(place).appendPath(e.size.toString()).appendPath(e.path).build()

        internal fun parse(uri: Uri): Remote? {
            val segs = uri.pathSegments
            if (segs.size != 3) return null
            val size = segs[1].toLongOrNull() ?: return null
            val path = segs[2]
            if (segs[0].isEmpty() || path.isEmpty() || size < 0) return null
            return Remote(segs[0], path, size)
        }

        fun mimeOf(name: String): String =
            MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase()) ?: "application/octet-stream"
    }
}
