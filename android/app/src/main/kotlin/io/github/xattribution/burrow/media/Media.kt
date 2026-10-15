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
import io.github.xattribution.burrow.data.BurrowFs
import io.github.xattribution.burrow.data.Opened
import io.github.xattribution.burrow.data.Place
import io.github.xattribution.burrow.data.PlaceFs
import io.github.xattribution.burrow.data.Places
import io.github.xattribution.burrow.data.PhoneFs
import android.media.ThumbnailUtils
import android.util.Size
import coil3.asImage
import coil3.fetch.ImageFetchResult
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okio.Path.Companion.toOkioPath
import kotlinx.coroutines.runBlocking
import okio.Buffer
import okio.FileSystem
import java.io.IOException

/**
 * Thumbnails: from Burrow's own previews through the encrypted channel, or made
 * on the phone from the picture itself for an SMB share. Coil caches them.
 */
class ThumbFetcher(private val places: Places, private val t: Thumb) : Fetcher {
    /** A file's small preview; mtime makes a changed file a new cache entry. */
    data class Thumb(val path: String, val mtime: Long, val place: String = Place.HOME)

    override suspend fun fetch(): FetchResult? {
        val fs = places.fs(t.place)
        if (fs is PhoneFs) {
            val f = fs.file(t.path)
            if (videoExt(t.path)) {
                // a frame from the video, the way the gallery shows it
                val bmp = withContext(Dispatchers.IO) { ThumbnailUtils.createVideoThumbnail(f, Size(320, 320), null) }
                return ImageFetchResult(bmp.asImage(), true, CoilSource.DISK)
            }
            return SourceFetchResult(ImageSource(f.toOkioPath(), FileSystem.SYSTEM), null, CoilSource.DISK)
        }
        if (fs is BurrowFs) {
            val bytes = fs.conn.use { it.thumb(t.path) } ?: throw IOException("no preview")
            return SourceFetchResult(ImageSource(Buffer().write(bytes), FileSystem.SYSTEM), "image/jpeg", CoilSource.NETWORK)
        }
        return SourceFetchResult(ImageSource(readCapped(fs, t.path), FileSystem.SYSTEM), null, CoilSource.NETWORK)
    }

    class Factory(private val places: Places) : Fetcher.Factory<Thumb> {
        override fun create(data: Thumb, options: Options, imageLoader: ImageLoader) = ThumbFetcher(places, data)
    }

    class ThumbKeyer : Keyer<Thumb> {
        override fun key(data: Thumb, options: Options) = "thumb:${data.place}:${data.mtime}:${data.path}"
    }
}

/** Full-size images, streamed from wherever they are. */
class FileImageFetcher(private val places: Places, private val img: Image) : Fetcher {
    data class Image(val path: String, val mtime: Long, val place: String = Place.HOME)

    override suspend fun fetch(): FetchResult {
        val fs = places.fs(img.place)
        if (fs is PhoneFs) return SourceFetchResult(ImageSource(fs.file(img.path).toOkioPath(), FileSystem.SYSTEM), null, CoilSource.DISK)
        return SourceFetchResult(ImageSource(readCapped(fs, img.path), FileSystem.SYSTEM), null, CoilSource.NETWORK)
    }

    class Factory(private val places: Places) : Fetcher.Factory<Image> {
        override fun create(data: Image, options: Options, imageLoader: ImageLoader) = FileImageFetcher(places, data)
    }

    class ImageKeyer : Keyer<Image> {
        override fun key(data: Image, options: Options) = "file:${data.place}:${data.mtime}:${data.path}"
    }
}

private fun videoExt(path: String) = path.substringAfterLast('.', "").lowercase() in setOf("mp4", "mov", "mkv", "webm", "m4v", "3gp", "avi")

/** A picture's bytes, up to 60 MB (bigger isn't worth decoding on a phone). */
private suspend fun readCapped(fs: PlaceFs, path: String): Buffer {
    val buf = Buffer()
    fs.open(path).use { o ->
        while (buf.size < 60L shl 20) {
            if (o.source.read(buf, 256 * 1024) < 0) return buf
        }
    }
    throw IOException("too big to preview")
}

/**
 * Video and audio for Media3, streamed through the channel with range requests,
 * so playback starts at once and seeking works without downloading everything.
 */
@UnstableApi
class ChannelDataSource(private val places: Places) : BaseDataSource(true) {
    private var opened: Opened? = null
    private var uri: Uri? = null
    private var left = C.LENGTH_UNSET.toLong()

    override fun open(dataSpec: DataSpec): Long {
        transferInitializing(dataSpec)
        uri = dataSpec.uri
        val path = dataSpec.uri.getQueryParameter("path") ?: throw IOException("no path")
        val place = dataSpec.uri.getQueryParameter("place") ?: Place.HOME
        val o = runBlocking { places.fs(place).open(path, from = dataSpec.position) }
        opened = o
        val rest = if (o.size >= 0) o.size - dataSpec.position else C.LENGTH_UNSET.toLong()
        left = if (dataSpec.length != C.LENGTH_UNSET.toLong()) dataSpec.length else rest
        transferStarted(dataSpec)
        return left
    }

    override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
        if (length == 0) return 0
        if (left == 0L) return C.RESULT_END_OF_INPUT
        val want = if (left > 0) minOf(left, length.toLong()).toInt() else length
        val n = opened?.source?.read(buffer, offset, want) ?: return C.RESULT_END_OF_INPUT
        if (n < 0) return C.RESULT_END_OF_INPUT
        if (left > 0) left -= n
        bytesTransferred(n)
        return n
    }

    override fun getUri(): Uri? = uri

    override fun close() {
        if (opened != null) {
            opened?.close()
            opened = null
            transferEnded()
        }
        uri = null
    }

    class Factory(private val places: Places) : DataSource.Factory {
        override fun createDataSource(): DataSource = ChannelDataSource(places)
    }

    companion object {
        fun uriFor(path: String, place: String = Place.HOME): Uri =
            Uri.Builder().scheme("burrow").authority("file").appendQueryParameter("path", path).appendQueryParameter("place", place).build()
    }
}
