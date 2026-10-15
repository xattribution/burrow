package io.github.xattribution.burrow.data

import android.content.ContentResolver
import android.content.ContentUris
import android.content.Context
import android.net.Uri
import android.os.Build
import android.provider.DocumentsContract
import android.provider.MediaStore
import io.github.xattribution.burrow.core.LocalEntry
import io.github.xattribution.burrow.core.LocalTree
import io.github.xattribution.burrow.graph
import okio.BufferedSink
import okio.Source
import okio.buffer
import okio.sink
import okio.source
import java.io.FileNotFoundException

/** The local side of a pair, from what is stored in its row. */
fun localTreeFor(context: Context, uri: String): LocalTree = when {
    uri.startsWith(MediaTree.PREFIX) -> MediaTree(context.contentResolver, context.checkSelfPermission(android.Manifest.permission.ACCESS_MEDIA_LOCATION) == android.content.pm.PackageManager.PERMISSION_GRANTED)
    uri.startsWith("file:") -> java.io.File(Uri.parse(uri).path ?: "/").let { root ->
        PhoneTree(root, volumeTop = phoneVolumes(context).any { it.root.absolutePath == root.absolutePath }) { context.graph.phoneAccess() }
    }
    else -> SafTree(context.contentResolver, Uri.parse(uri))
}

/**
 * Any folder on the phone, read as plain files with All files access: Downloads,
 * the top of the storage or a memory card, which the system folder picker won't
 * hand out. Hidden files and Burrow's own partial downloads aren't synced.
 */
class PhoneTree(private val root: java.io.File, private val volumeTop: Boolean, private val access: () -> Boolean) : LocalTree {
    private val files = io.github.xattribution.burrow.core.FileTree(root)

    private fun check() {
        if (!access()) throw java.io.IOException("Burrow no longer has access to all files. Turn it back on to sync ${root.name}.")
        if (!root.isDirectory) throw java.io.IOException("${root.path} isn't there any more (a memory card that was taken out?).")
    }

    override suspend fun list(): List<LocalEntry> {
        check()
        // at the top of a volume, Android/ holds apps' own folders, which aren't the user's to sync
        return files.list().filter { e -> e.path.split('/').none { it.startsWith(".") } && !(volumeTop && (e.path == "Android" || e.path.startsWith("Android/"))) }
    }

    override suspend fun stat(path: String): LocalEntry? = files.stat(path)
    override fun open(path: String): Source = files.open(path)
    override suspend fun write(path: String, mtime: Long, body: (BufferedSink) -> Unit): LocalEntry {
        check()
        return files.write(path, mtime, body)
    }
    override suspend fun mkdirs(path: String) = files.mkdirs(path)
    override suspend fun delete(path: String) = files.delete(path)
    override suspend fun rename(from: String, to: String) = files.rename(from, to)
}

/**
 * A folder the user picked with the system's folder picker (Storage Access
 * Framework). Burrow only ever sees that folder and what is inside it.
 */
class SafTree(private val cr: ContentResolver, private val tree: Uri) : LocalTree {
    private val rootId = DocumentsContract.getTreeDocumentId(tree)
    private val ids = mutableMapOf("" to rootId)
    private val dirs = mutableSetOf("")

    private fun doc(id: String) = DocumentsContract.buildDocumentUriUsingTree(tree, id)

    private data class Child(val id: String, val name: String, val dir: Boolean, val size: Long, val mtime: Long)

    private fun children(parentId: String): List<Child> {
        val out = mutableListOf<Child>()
        val cols = arrayOf(
            DocumentsContract.Document.COLUMN_DOCUMENT_ID, DocumentsContract.Document.COLUMN_DISPLAY_NAME,
            DocumentsContract.Document.COLUMN_MIME_TYPE, DocumentsContract.Document.COLUMN_SIZE, DocumentsContract.Document.COLUMN_LAST_MODIFIED,
        )
        cr.query(DocumentsContract.buildChildDocumentsUriUsingTree(tree, parentId), cols, null, null, null)?.use { c ->
            while (c.moveToNext()) {
                val dir = c.getString(2) == DocumentsContract.Document.MIME_TYPE_DIR
                out += Child(c.getString(0), c.getString(1) ?: continue, dir, if (dir || c.isNull(3)) 0 else c.getLong(3), if (c.isNull(4)) 0 else c.getLong(4))
            }
        }
        return out
    }

    override suspend fun list(): List<LocalEntry> {
        val out = mutableListOf<LocalEntry>()
        fun walk(parentId: String, prefix: String) {
            for (ch in children(parentId).sortedBy { it.name }) {
                val rel = if (prefix.isEmpty()) ch.name else "$prefix/${ch.name}"
                ids[rel] = ch.id
                if (ch.dir) dirs += rel
                out += LocalEntry(rel, ch.dir, ch.size, ch.mtime)
                if (ch.dir) walk(ch.id, rel)
            }
        }
        walk(rootId, "")
        return out
    }

    /** Finds a path's document id, looking it up level by level if it isn't known yet. */
    private fun idOf(path: String): String? {
        ids[path]?.let { return it }
        val parent = path.substringBeforeLast('/', "")
        val pid = idOf(parent) ?: return null
        val name = path.substringAfterLast('/')
        val ch = children(pid).firstOrNull { it.name == name } ?: return null
        ids[path] = ch.id
        if (ch.dir) dirs += path
        return ch.id
    }

    override suspend fun stat(path: String): LocalEntry? {
        val id = idOf(path) ?: return null
        val cols = arrayOf(DocumentsContract.Document.COLUMN_MIME_TYPE, DocumentsContract.Document.COLUMN_SIZE, DocumentsContract.Document.COLUMN_LAST_MODIFIED)
        return try {
            cr.query(doc(id), cols, null, null, null)?.use { c ->
                if (!c.moveToFirst()) return null
                val dir = c.getString(0) == DocumentsContract.Document.MIME_TYPE_DIR
                LocalEntry(path, dir, if (dir || c.isNull(1)) 0 else c.getLong(1), if (c.isNull(2)) 0 else c.getLong(2))
            }
        } catch (e: FileNotFoundException) {
            ids.remove(path)
            null
        } catch (e: IllegalArgumentException) {
            ids.remove(path)
            null
        }
    }

    override fun open(path: String): Source {
        val id = idOf(path) ?: throw FileNotFoundException(path)
        return (cr.openInputStream(doc(id)) ?: throw FileNotFoundException(path)).source()
    }

    override suspend fun mkdirs(path: String) {
        if (path.isEmpty() || idOf(path) != null) return
        val parent = path.substringBeforeLast('/', "")
        mkdirs(parent)
        val pid = idOf(parent) ?: throw FileNotFoundException(parent)
        val uri = DocumentsContract.createDocument(cr, doc(pid), DocumentsContract.Document.MIME_TYPE_DIR, path.substringAfterLast('/'))
            ?: throw FileNotFoundException("couldn't create $path")
        ids[path] = DocumentsContract.getDocumentId(uri)
        dirs += path
    }

    override suspend fun write(path: String, mtime: Long, body: (BufferedSink) -> Unit): LocalEntry {
        val parent = path.substringBeforeLast('/', "")
        val name = path.substringAfterLast('/')
        mkdirs(parent)
        val pid = idOf(parent) ?: throw FileNotFoundException(parent)
        // write next to it under a hidden name, then swap it in
        val tmpName = ".burrow-dl-$name"
        children(pid).firstOrNull { it.name == tmpName }?.let { DocumentsContract.deleteDocument(cr, doc(it.id)) }
        val tmp = DocumentsContract.createDocument(cr, doc(pid), "application/octet-stream", tmpName) ?: throw FileNotFoundException("couldn't write $path")
        try {
            (cr.openOutputStream(tmp, "wt") ?: throw FileNotFoundException(path)).sink().buffer().use(body)
            idOf(path)?.let { DocumentsContract.deleteDocument(cr, doc(it)) }
            ids.remove(path)
            val renamed = DocumentsContract.renameDocument(cr, tmp, name) ?: tmp
            ids[path] = DocumentsContract.getDocumentId(renamed)
        } catch (e: Exception) {
            runCatching { DocumentsContract.deleteDocument(cr, tmp) }
            throw e
        }
        return stat(path) ?: throw FileNotFoundException(path)
    }

    override suspend fun delete(path: String): Boolean {
        val id = idOf(path) ?: return false
        if (path in dirs && children(id).isNotEmpty()) return false
        val ok = runCatching { DocumentsContract.deleteDocument(cr, doc(id)) }.getOrDefault(false)
        if (ok) {
            ids.remove(path)
            dirs.remove(path)
        }
        return ok
    }

    override suspend fun rename(from: String, to: String): Boolean {
        if (from.substringBeforeLast('/', "") != to.substringBeforeLast('/', "") || idOf(to) != null) return false
        val id = idOf(from) ?: return false
        val renamed = runCatching { DocumentsContract.renameDocument(cr, doc(id), to.substringAfterLast('/')) }.getOrNull() ?: return false
        ids.remove(from)
        ids[to] = DocumentsContract.getDocumentId(renamed)
        return true
    }
}

/**
 * The phone's photos and videos, from the media library. Paths keep the folders
 * they are in on the phone (DCIM/Camera/IMG_1.jpg). Read-only: it is backed up,
 * never written to.
 */
class MediaTree(private val cr: ContentResolver, private val withLocation: Boolean) : LocalTree {
    private val ids = mutableMapOf<String, Pair<Long, Boolean>>() // path -> id, is video

    override suspend fun list(): List<LocalEntry> {
        val out = mutableListOf<LocalEntry>()
        for ((uri, video) in listOf(MediaStore.Images.Media.EXTERNAL_CONTENT_URI to false, MediaStore.Video.Media.EXTERNAL_CONTENT_URI to true)) {
            val cols = arrayOf(MediaStore.MediaColumns._ID, MediaStore.MediaColumns.RELATIVE_PATH, MediaStore.MediaColumns.DISPLAY_NAME, MediaStore.MediaColumns.SIZE, MediaStore.MediaColumns.DATE_MODIFIED)
            cr.query(uri, cols, "${MediaStore.MediaColumns.IS_PENDING} = 0", null, null)?.use { c ->
                while (c.moveToNext()) {
                    val name = c.getString(2) ?: continue
                    val rel = (c.getString(1) ?: "").trim('/')
                    val path = if (rel.isEmpty()) name else "$rel/$name"
                    ids[path] = c.getLong(0) to video
                    out += LocalEntry(path, false, c.getLong(3), c.getLong(4) * 1000)
                }
            }
        }
        return out
    }

    private fun uri(path: String): Uri {
        if (ids.isEmpty()) kotlinx.coroutines.runBlocking { list() }
        val (id, video) = ids[path] ?: throw FileNotFoundException(path)
        val base = if (video) MediaStore.Video.Media.EXTERNAL_CONTENT_URI else MediaStore.Images.Media.EXTERNAL_CONTENT_URI
        val u = ContentUris.withAppendedId(base, id)
        // the original bytes, location included, when the user allowed it
        return if (withLocation && Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) runCatching { MediaStore.setRequireOriginal(u) }.getOrDefault(u) else u
    }

    override suspend fun stat(path: String): LocalEntry? {
        val (id, video) = ids[path] ?: return null
        val base = if (video) MediaStore.Video.Media.EXTERNAL_CONTENT_URI else MediaStore.Images.Media.EXTERNAL_CONTENT_URI
        cr.query(ContentUris.withAppendedId(base, id), arrayOf(MediaStore.MediaColumns.SIZE, MediaStore.MediaColumns.DATE_MODIFIED), null, null, null)?.use { c ->
            if (c.moveToFirst()) return LocalEntry(path, false, c.getLong(0), c.getLong(1) * 1000)
        }
        return null
    }

    override fun open(path: String): Source = (cr.openInputStream(uri(path)) ?: throw FileNotFoundException(path)).source()

    override suspend fun write(path: String, mtime: Long, body: (BufferedSink) -> Unit): LocalEntry = throw UnsupportedOperationException("the photo library is backed up, not written to")
    override suspend fun mkdirs(path: String) = Unit
    override suspend fun delete(path: String) = false
    override suspend fun rename(from: String, to: String) = false

    companion object {
        const val PREFIX = "media:"
        const val ALL = "media:all"
    }
}
