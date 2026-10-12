package io.github.xattribution.burrow.media

import android.net.Uri
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.BaseDataSource
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSpec
import coil3.ImageLoader
import coil3.decode.DataSource as CoilSource
import coil3.decode.ImageSource
import coil3.fetch.FetchResult
import coil3.fetch.Fetcher
import coil3.fetch.SourceFetchResult
import coil3.key.Keyer
import coil3.request.Options
import io.github.xattribution.burrow.core.Response
import io.github.xattribution.burrow.data.Connection
import kotlinx.coroutines.runBlocking
import okio.Buffer
import okio.FileSystem
import java.io.IOException

/** Thumbnails come through the encrypted channel, then Coil caches them. */
class ThumbFetcher(private val conn: Connection, private val t: Thumb) : Fetcher {
    /** A file's small preview; mtime makes a changed file a new cache entry. */
    data class Thumb(val path: String, val mtime: Long)

    override suspend fun fetch(): FetchResult? {
        val bytes = conn.use { it.thumb(t.path) } ?: throw IOException("no preview")
        return SourceFetchResult(ImageSource(Buffer().write(bytes), FileSystem.SYSTEM), "image/jpeg", CoilSource.NETWORK)
    }

    class Factory(private val conn: Connection) : Fetcher.Factory<Thumb> {
        override fun create(data: Thumb, options: Options, imageLoader: ImageLoader) = ThumbFetcher(conn, data)
    }

    class ThumbKeyer : Keyer<Thumb> {
        override fun key(data: Thumb, options: Options) = "thumb:${data.mtime}:${data.path}"
    }
}

/** Full-size images, streamed through the channel. */
class FileImageFetcher(private val conn: Connection, private val img: Image) : Fetcher {
    data class Image(val path: String, val mtime: Long)

    override suspend fun fetch(): FetchResult {
        val r = conn.use { it.raw(img.path) }
        val buf = Buffer()
        r.use { buf.writeAll(it.body) }
        return SourceFetchResult(ImageSource(buf, FileSystem.SYSTEM), r.header("Content-Type"), CoilSource.NETWORK)
    }

    class Factory(private val conn: Connection) : Fetcher.Factory<Image> {
        override fun create(data: Image, options: Options, imageLoader: ImageLoader) = FileImageFetcher(conn, data)
    }

    class ImageKeyer : Keyer<Image> {
        override fun key(data: Image, options: Options) = "file:${data.mtime}:${data.path}"
    }
}

/**
 * Video and audio for Media3, streamed through the channel with range requests,
 * so playback starts at once and seeking works without downloading everything.
 */
@UnstableApi
class ChannelDataSource(private val conn: Connection) : BaseDataSource(true) {
    private var resp: Response? = null
    private var uri: Uri? = null
    private var left = C.LENGTH_UNSET.toLong()

    override fun open(dataSpec: DataSpec): Long {
        transferInitializing(dataSpec)
        uri = dataSpec.uri
        val path = dataSpec.uri.getQueryParameter("path") ?: throw IOException("no path")
        val r = runBlocking { conn.use { it.raw(path, from = dataSpec.position) } }
        resp = r
        val total = r.header("Content-Length")?.toLongOrNull() ?: C.LENGTH_UNSET.toLong()
        left = if (dataSpec.length != C.LENGTH_UNSET.toLong()) dataSpec.length else total
        transferStarted(dataSpec)
        return left
    }

    override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
        if (length == 0) return 0
        if (left == 0L) return C.RESULT_END_OF_INPUT
        val want = if (left > 0) minOf(left, length.toLong()).toInt() else length
        val n = resp?.body?.read(buffer, offset, want) ?: return C.RESULT_END_OF_INPUT
        if (n < 0) return C.RESULT_END_OF_INPUT
        if (left > 0) left -= n
        bytesTransferred(n)
        return n
    }

    override fun getUri(): Uri? = uri

    override fun close() {
        if (resp != null) {
            resp?.close()
            resp = null
            transferEnded()
        }
        uri = null
    }

    class Factory(private val conn: Connection) : DataSource.Factory {
        override fun createDataSource(): DataSource = ChannelDataSource(conn)
    }

    companion object {
        fun uriFor(path: String): Uri = Uri.Builder().scheme("burrow").authority("file").appendQueryParameter("path", path).build()
    }
}
