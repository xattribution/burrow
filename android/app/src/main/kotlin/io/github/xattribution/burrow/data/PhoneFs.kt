package io.github.xattribution.burrow.data

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.storage.StorageManager
import android.provider.Settings
import io.github.xattribution.burrow.core.Entry
import io.github.xattribution.burrow.core.Listing
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okio.Source
import okio.buffer
import okio.sink
import okio.source
import java.io.File
import java.io.FileInputStream
import java.io.IOException

/** Thrown when the phone's own storage is opened before Burrow may read it. */
class NoPhoneAccessException : IOException("Burrow needs access to all files to show this phone's storage.")

/**
 * Access to the phone's own storage. From Android 11 that is "All files access"
 * (a switch in the system settings, not a pop-up); Android 10 uses the older
 * storage permission. Either way Android keeps other apps' private folders
 * (Android/data, Android/obb) to themselves.
 */
object PhoneAccess {
    fun granted(ctx: Context): Boolean =
        if (Build.VERSION.SDK_INT >= 30) Environment.isExternalStorageManager()
        else ctx.checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED

    /** The settings screen with Burrow's "All files access" switch (Android 11 and later). */
    fun settingsIntent(ctx: Context): Intent =
        Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:${ctx.packageName}"))

    /** What Android 10 asks for instead. */
    val legacyPermissions = arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE, Manifest.permission.WRITE_EXTERNAL_STORAGE)
}

/** A storage volume of this phone: its own storage, a memory card or a USB drive. */
data class PhoneVolume(val id: String, val name: String, val root: File, val removable: Boolean)

/** The phone's mounted storage volumes, its own storage first. */
fun phoneVolumes(ctx: Context): List<PhoneVolume> {
    val out = mutableListOf<PhoneVolume>()
    val sm = ctx.getSystemService(StorageManager::class.java)
    val primary = Environment.getExternalStorageDirectory()
    out += PhoneVolume(Place.PHONE, "This phone", primary, false)
    if (Build.VERSION.SDK_INT >= 30 && sm != null) {
        for (v in sm.storageVolumes) {
            val dir = v.directory ?: continue
            if (v.isPrimary || v.state != Environment.MEDIA_MOUNTED || dir == primary) continue
            val key = (v.uuid ?: dir.name).lowercase().filter { it.isLetterOrDigit() || it == '-' }
            out += PhoneVolume("vol-$key", v.getDescription(ctx).ifBlank { "Memory card" }, dir, true)
        }
    }
    return out
}

/**
 * A folder tree on the phone itself, read and written as plain files. Deletes are
 * permanent (the phone's own file manager has the trash, if any). Hidden files
 * (names starting with a dot) aren't listed.
 */
class PhoneFs(override val id: String, val root: File, private val access: () -> Boolean) : PlaceFs {
    override val isBurrow = false
    override val canSearch = true

    /** The file at [path]; paths come from listings, but ".." never leaves the volume. */
    fun file(path: String): File {
        val segs = path.split('/').filter { it.isNotEmpty() }
        if (segs.any { it == ".." || it == "." }) throw IOException("That path isn't on this phone.")
        return if (segs.isEmpty()) root else File(root, segs.joinToString("/"))
    }

    private fun check() {
        if (!access()) throw NoPhoneAccessException()
    }

    private fun entry(path: String, f: File) = entryOf(path, f.name.ifEmpty { path }, f.isDirectory, f.length(), f.lastModified() / 1000)

    override suspend fun ls(path: String): Listing = withContext(Dispatchers.IO) {
        check()
        val dir = file(path)
        val kids = dir.listFiles() ?: throw IOException(
            when {
                !dir.exists() -> "It isn't there any more."
                isAppPrivate(path) -> "Android keeps this folder to the apps it belongs to."
                else -> "Android doesn't let Burrow open this folder."
            },
        )
        val entries = kids.filter { !it.name.startsWith(".") }
            .map { entry(join(path, it.name), it) }
            .sortedWith(compareBy<Entry>({ !it.dir }, { it.name.lowercase() }))
        Listing(path = path, entries = entries, canShare = false, canWrite = dir.canWrite())
    }

    override suspend fun stat(path: String): Entry = withContext(Dispatchers.IO) {
        check()
        val f = file(path)
        if (!f.exists()) throw IOException("It isn't there any more.")
        entry(path, f)
    }

    override suspend fun open(path: String, from: Long): Opened = withContext(Dispatchers.IO) {
        check()
        val f = file(path)
        val input = FileInputStream(f)
        if (from > 0) input.channel.position(from)
        Opened(f.length(), input.source().buffer(), input::close)
    }

    override suspend fun put(dir: String, name: String, size: Long, mtimeMs: Long, open: () -> Source, progress: (Long) -> Unit): String = withContext(Dispatchers.IO) {
        check()
        val parent = file(dir)
        parent.mkdirs()
        val ext = name.substringAfterLast('.', "").let { if (it.isEmpty() || it == name) "" else ".$it" }
        val base = name.removeSuffix(ext)
        for (i in 1 until 1000) {
            val cand = if (i == 1) name else "$base ($i)$ext"
            val f = File(parent, cand)
            // createNewFile is atomic, so two copies at once never share a name
            if (!f.createNewFile()) continue
            var ok = false
            try {
                f.sink().buffer().use { out ->
                    open().buffer().use { src ->
                        val buf = okio.Buffer()
                        var sent = 0L
                        while (true) {
                            val n = src.read(buf, 256L * 1024)
                            if (n < 0) break
                            out.write(buf, n)
                            sent += n
                            progress(sent)
                        }
                        if (size >= 0 && sent != size) throw IOException("The file changed while it was being copied.")
                    }
                }
                if (mtimeMs > 0) f.setLastModified(mtimeMs)
                ok = true
            } finally {
                if (!ok) f.delete()
            }
            return@withContext join(dir, cand)
        }
        throw IOException("Too many files with that name.")
    }

    override suspend fun mkdir(path: String) = withContext(Dispatchers.IO) {
        check()
        val f = file(path)
        if (f.exists()) throw IOException("Something with that name is already there.")
        if (!f.mkdirs()) throw IOException("Couldn't make that folder here.")
    }

    override suspend fun move(from: String, to: String): String = withContext(Dispatchers.IO) {
        check()
        val src = file(from)
        val dst = file(to)
        if (dst.exists()) throw IOException("Something with that name is already there.")
        dst.parentFile?.mkdirs()
        if (!src.renameTo(dst)) throw IOException("Couldn't move it there.")
        to
    }

    override suspend fun delete(path: String) = withContext(Dispatchers.IO) {
        check()
        if (path.isEmpty()) throw IOException("That's the whole volume.")
        val f = file(path)
        if (!f.deleteRecursively()) throw IOException("Couldn't delete all of it.")
        Unit
    }

    /** Names under [under] that contain every word of [query], folders first; at most [limit]. */
    override suspend fun find(under: String, query: String, limit: Int): List<Entry> = withContext(Dispatchers.IO) {
        check()
        val words = query.lowercase().split(' ').filter { it.isNotBlank() }
        if (words.isEmpty()) return@withContext emptyList()
        val start = file(under)
        val out = mutableListOf<Entry>()
        val walk = start.walkTopDown()
            .onEnter { d -> d == start || (!d.name.startsWith(".") && !isAppPrivate(d.relativeTo(root).invariantSeparatorsPath)) }
        for (f in walk) {
            if (out.size >= limit) break
            if (f == start || f.name.startsWith(".")) continue
            val name = f.name.lowercase()
            if (words.all { it in name }) out += entry(f.relativeTo(root).invariantSeparatorsPath, f)
        }
        out.sortedWith(compareBy<Entry>({ !it.dir }, { it.name.lowercase() }))
    }

    private fun isAppPrivate(path: String) = path == "Android/data" || path == "Android/obb" || path.startsWith("Android/data/") || path.startsWith("Android/obb/")
}

private fun join(dir: String, name: String) = if (dir.isEmpty()) name else "$dir/$name"
