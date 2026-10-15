package io.github.xattribution.burrow.data

import android.content.ContentResolver
import android.content.Context
import android.database.Cursor
import android.net.Uri
import android.provider.DocumentsContract
import android.provider.DocumentsContract.Document
import io.github.xattribution.burrow.core.Entry
import io.github.xattribution.burrow.core.Listing
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okio.Source
import okio.buffer
import okio.sink
import okio.source
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap

/**
 * A folder another app shares through Android's document picker: a cloud drive
 * app (Google Drive, OneDrive, Nextcloud), Termux, a USB drive the system shows,
 * or any folder the phone's own storage lets through. Burrow sees only that
 * folder and what is inside it, for as long as the grant lasts.
 */
class DocFs(override val id: String, ctx: Context, val tree: Uri) : PlaceFs {
    override val isBurrow = false
    override val canSearch = true
    private val cr: ContentResolver = ctx.contentResolver
    private val rootId = DocumentsContract.getTreeDocumentId(tree)
    private val ids = ConcurrentHashMap<String, String>().apply { put("", rootId) }

    private class Kid(val id: String, val name: String, val dir: Boolean, val size: Long, val mtime: Long, val flags: Int)

    private val cols = arrayOf(Document.COLUMN_DOCUMENT_ID, Document.COLUMN_DISPLAY_NAME, Document.COLUMN_MIME_TYPE, Document.COLUMN_SIZE, Document.COLUMN_LAST_MODIFIED, Document.COLUMN_FLAGS)

    private fun Cursor.kid() = Kid(
        getString(0), getString(1) ?: getString(0), getString(2) == Document.MIME_TYPE_DIR,
        if (isNull(3)) 0 else getLong(3), if (isNull(4)) 0 else getLong(4), if (isNull(5)) 0 else getInt(5),
    )

    private fun doc(docId: String) = DocumentsContract.buildDocumentUriUsingTree(tree, docId)

    private fun kids(parentId: String): List<Kid> {
        val out = mutableListOf<Kid>()
        val c = try {
            cr.query(DocumentsContract.buildChildDocumentsUriUsingTree(tree, parentId), cols, null, null, null)
        } catch (e: SecurityException) {
            throw IOException("Burrow can't open this folder any more. Add it again.", e)
        } ?: throw IOException("The app that holds this folder didn't answer.")
        c.use { while (it.moveToNext()) out += it.kid() }
        return out
    }

    private fun one(docId: String): Kid {
        val c = try {
            cr.query(doc(docId), cols, null, null, null)
        } catch (e: SecurityException) {
            throw IOException("Burrow can't open this folder any more. Add it again.", e)
        }
        return c?.use { if (it.moveToFirst()) it.kid() else null } ?: throw IOException("It isn't there any more.")
    }

    private fun idOf(path: String): String {
        ids[path]?.let { return it }
        val parent = path.substringBeforeLast('/', "")
        val name = path.substringAfterLast('/')
        val k = kids(idOf(parent)).firstOrNull { it.name == name } ?: throw IOException("It isn't there any more.")
        ids[path] = k.id
        return k.id
    }

    private fun entry(path: String, k: Kid) = entryOf(path, k.name, k.dir, k.size, k.mtime / 1000)

    override suspend fun ls(path: String): Listing = withContext(Dispatchers.IO) {
        val pid = idOf(path)
        val entries = kids(pid).filter { !it.name.startsWith(".") }.map { k ->
            val p = join(path, k.name)
            ids[p] = k.id
            entry(p, k)
        }.sortedWith(compareBy<Entry>({ !it.dir }, { it.name.lowercase() }))
        Listing(path = path, entries = entries, canShare = false, canWrite = one(pid).flags and Document.FLAG_DIR_SUPPORTS_CREATE != 0)
    }

    override suspend fun stat(path: String): Entry = withContext(Dispatchers.IO) { entry(path, one(idOf(path))) }

    override suspend fun open(path: String, from: Long): Opened = withContext(Dispatchers.IO) {
        val k = one(idOf(path))
        val input = cr.openInputStream(doc(k.id)) ?: throw IOException("The app that holds this file didn't open it.")
        var skipped = 0L
        while (skipped < from) {
            val n = input.skip(from - skipped)
            if (n <= 0) break
            skipped += n
        }
        Opened(k.size, input.source().buffer(), input::close)
    }

    override suspend fun put(dir: String, name: String, size: Long, mtimeMs: Long, open: () -> Source, progress: (Long) -> Unit): String = withContext(Dispatchers.IO) {
        val parent = doc(idOf(dir))
        val made = DocumentsContract.createDocument(cr, parent, RemoteFileProvider.mimeOf(name), name)
            ?: throw IOException("The app that holds this folder didn't make the file.")
        var ok = false
        try {
            (cr.openOutputStream(made, "w") ?: throw IOException("The app that holds this folder didn't take the file.")).sink().buffer().use { out ->
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
            ok = true
        } finally {
            if (!ok) runCatching { DocumentsContract.deleteDocument(cr, made) }
        }
        // the app may have renamed it to avoid a clash ("name (1).ext")
        val docId = DocumentsContract.getDocumentId(made)
        val p = join(dir, one(docId).name)
        ids[p] = docId
        p
    }

    override suspend fun mkdir(path: String) = withContext(Dispatchers.IO) {
        val parent = path.substringBeforeLast('/', "")
        val name = path.substringAfterLast('/')
        val pid = idOf(parent)
        if (kids(pid).any { it.name == name }) throw IOException("Something with that name is already there.")
        val made = DocumentsContract.createDocument(cr, doc(pid), Document.MIME_TYPE_DIR, name) ?: throw IOException("Couldn't make that folder here.")
        ids[path] = DocumentsContract.getDocumentId(made)
    }

    override suspend fun move(from: String, to: String): String = withContext(Dispatchers.IO) {
        val srcId = idOf(from)
        val fromDir = from.substringBeforeLast('/', "")
        val toDir = to.substringBeforeLast('/', "")
        val newName = to.substringAfterLast('/')
        val destKids = kids(idOf(toDir))
        if (destKids.any { it.name == newName }) throw IOException("Something with that name is already there.")
        var uri = doc(srcId)
        if (fromDir != toDir) {
            uri = DocumentsContract.moveDocument(cr, uri, doc(idOf(fromDir)), doc(idOf(toDir)))
                ?: throw IOException("The app that holds these files can't move them between folders.")
        }
        if (from.substringAfterLast('/') != newName) {
            uri = DocumentsContract.renameDocument(cr, uri, newName) ?: uri
        }
        ids.keys.removeAll { it == from || it.startsWith("$from/") }
        ids[to] = DocumentsContract.getDocumentId(uri)
        to
    }

    override suspend fun delete(path: String) = withContext(Dispatchers.IO) {
        if (path.isEmpty()) throw IOException("That's the whole folder; remove the connection instead.")
        if (!DocumentsContract.deleteDocument(cr, doc(idOf(path)))) throw IOException("The app that holds this file didn't delete it.")
        ids.keys.removeAll { it == path || it.startsWith("$path/") }
        Unit
    }

    override suspend fun find(under: String, query: String, limit: Int): List<Entry> = withContext(Dispatchers.IO) {
        val words = query.lowercase().split(' ').filter { it.isNotBlank() }
        val out = mutableListOf<Entry>()
        if (words.isEmpty()) return@withContext out
        fun walk(dirId: String, prefix: String, depth: Int) {
            if (depth > 12) return
            for (k in kids(dirId)) {
                if (out.size >= limit) return
                if (k.name.startsWith(".")) continue
                val p = join(prefix, k.name)
                ids[p] = k.id
                if (words.all { it in k.name.lowercase() }) out += entry(p, k)
                if (k.dir) walk(k.id, p, depth + 1)
            }
        }
        walk(idOf(under), under, 0)
        out
    }

    companion object {
        /** A name for a picked folder: its own name, or the app's when it is the app's top. */
        fun nameOf(ctx: Context, tree: Uri): String {
            val docId = DocumentsContract.getTreeDocumentId(tree)
            val name = runCatching {
                ctx.contentResolver.query(DocumentsContract.buildDocumentUriUsingTree(tree, docId), arrayOf(Document.COLUMN_DISPLAY_NAME), null, null, null)?.use { c ->
                    if (c.moveToFirst()) c.getString(0) else null
                }
            }.getOrNull()
            return name?.takeIf { it.isNotBlank() } ?: docId.substringAfterLast(':').substringAfterLast('/').ifBlank { "App folder" }
        }
    }
}

private fun join(dir: String, name: String) = if (dir.isEmpty()) name else "$dir/$name"
