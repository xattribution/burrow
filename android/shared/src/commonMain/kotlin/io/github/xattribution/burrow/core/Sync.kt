package io.github.xattribution.burrow.core

import io.github.xattribution.burrow.core.db.BurrowDb
import io.github.xattribution.burrow.core.db.Pair as PairRow
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import okio.BufferedSink
import okio.Source
import okio.buffer

/** How a folder pair syncs. */
enum class SyncMode(val id: String) {
    /** Phone to Burrow. Everything on the phone ends up on Burrow; deleting on the phone deletes nothing. */
    BACKUP("backup"),

    /** Both ways: additions, changes and deletes travel in both directions. */
    TWO_WAY("two-way"),

    /** Burrow to phone: a copy of the folder on the phone. Edits made on the phone are kept as conflict copies. */
    DOWNLOAD("download");

    companion object {
        fun of(id: String) = entries.firstOrNull { it.id == id } ?: BACKUP
    }
}

data class LocalEntry(val path: String, val dir: Boolean, val size: Long, val mtime: Long) {
    val stamp get() = Stamp(size, mtime)
}

/** The phone side of a pair: a folder (Storage Access Framework) or the photo library. */
interface LocalTree {
    /** Everything under the root, recursively, as '/'-separated relative paths. */
    suspend fun list(): List<LocalEntry>
    suspend fun stat(path: String): LocalEntry?
    fun open(path: String): Source

    /** Writes a whole file, replacing what is there, creating folders on the way. Returns what is there now. */
    suspend fun write(path: String, mtime: Long, body: (BufferedSink) -> Unit): LocalEntry
    suspend fun mkdirs(path: String)

    /** Deletes a file, or a folder if it is empty. */
    suspend fun delete(path: String): Boolean
    suspend fun rename(from: String, to: String): Boolean
}

data class SyncProgress(val phase: String, val done: Int, val total: Int, val bytes: Long, val totalBytes: Long, val current: String)

data class SyncReport(
    val uploaded: Int = 0,
    val downloaded: Int = 0,
    val deletedRemote: Int = 0,
    val deletedLocal: Int = 0,
    val moved: Int = 0,
    val conflicts: Int = 0,
    val errors: List<String> = emptyList(),
    val listed: Boolean = true,
) {
    val changes get() = uploaded + downloaded + deletedRemote + deletedLocal + moved + conflicts
}

/**
 * Three-way sync: each side is compared with what both looked like when they
 * last agreed (the base), so the engine can tell an addition on one side from
 * a deletion on the other. Each step updates the base as it completes, so an
 * interrupted sync continues where it stopped.
 */
class SyncEngine(
    private val api: BurrowApi,
    private val db: BurrowDb,
    private val now: () -> Long = { nowSeconds() },
    private val conflictTag: () -> String = { "conflict" },
) {
    private val q get() = db.pairQueries
    private val uploader = Uploader(api)

    private data class Base(val dir: Boolean, val l: Stamp, val r: Stamp, val hash: String)
    private data class Remote(val dir: Boolean, val stamp: Stamp, val hash: String)

    private enum class Delta { SAME, CHANGED, GONE, NEW, ABSENT }

    private sealed interface Act { val path: String }
    private data class Upload(override val path: String, val conflict: String, val expect: Stamp?) : Act
    private data class Download(override val path: String, val replace: Stamp?) : Act
    private data class DelRemote(override val path: String, val expect: Stamp, val dir: Boolean) : Act
    private data class DelLocal(override val path: String, val expect: Stamp?, val dir: Boolean) : Act
    private data class Record(override val path: String, val l: Stamp, val r: Stamp, val hash: String, val dir: Boolean = false) : Act
    private data class Forget(override val path: String) : Act
    private data class KeepBoth(override val path: String) : Act
    private data class MoveRemote(override val path: String, val from: String) : Act
    private data class MkRemote(override val path: String) : Act
    private data class MkLocal(override val path: String) : Act

    private suspend fun run(pair: PairRow, local: LocalTree, progress: (SyncProgress) -> Unit): SyncReport {
        val mode = SyncMode.of(pair.mode)
        val root = pair.remote
        val base = q.baseOf(pair.id).executeAsList().associate { it.path to Base(it.dir != 0L, Stamp(it.l_size, it.l_mtime), Stamp(it.r_size, it.r_mtime), it.hash) }

        progress(SyncProgress("Checking Burrow", 0, 0, 0, 0, ""))
        // List Burrow's side only when something changed there since last time
        // (or every few hours, in case a change slipped past the change log).
        val fresh = now() - pair.last_full > 6 * 3600 || pair.cursor == 0L ||
            missingAsChanged { api.syncChanged(root, pair.cursor).changed }
        var excludes = DEFAULT_EXCLUDES
        var writable = true
        val remote: Map<String, Remote>
        if (fresh) {
            val l = try {
                api.syncList(root)
            } catch (e: ApiException) {
                if (e.status != 404) throw e
                // The folder on Burrow isn't there. A first sync makes it. A folder
                // that was there and is gone is never read as "everything was
                // deleted": a backup puts it all back, the other modes stop.
                if (mode == SyncMode.DOWNLOAD || (mode == SyncMode.TWO_WAY && base.isNotEmpty()))
                    throw ApiException(404, "The folder $root isn't on Burrow any more. Nothing on the phone was changed.")
                api.mkdir(root, parents = true)
                api.syncList(root)
            }
            excludes = l.excludes
            writable = l.writable
            remote = l.entries.filterNot { excluded(it.path, excludes) }.associate { it.path to Remote(it.dir, Stamp(it.size, it.mtime), it.hash) }
            q.setCursor(l.cursor, now(), pair.id)
        } else {
            remote = base.mapValues { Remote(it.value.dir, it.value.r, it.value.hash) }
        }

        progress(SyncProgress("Checking the phone", 0, 0, 0, 0, ""))
        val localList = local.list().filterNot { excluded(it.path, excludes) || it.path.split('/').any { s -> s.startsWith(".burrow-") || s.startsWith(".pending-") || s.startsWith(".trashed-") } }
        val lmap = localList.associateBy { it.path }

        // where both sides have a file of the same size but different times, compare contents
        val remoteHashed = remote.toMutableMap()
        for ((p, le) in lmap) {
            val re = remote[p] ?: continue
            if (le.dir || re.dir || le.size != re.stamp.size || re.hash.isNotEmpty() || closeTimes(le.mtime, re.stamp.mtime)) continue
            val b = base[p]
            if (b != null && (b.l == le.stamp || b.r == re.stamp)) continue // only one side changed: no comparison needed
            currentCoroutineContext().ensureActive()
            runCatching { api.syncHash("$root/$p").sha256 }.getOrNull()?.let { remoteHashed[p] = re.copy(hash = it) }
        }
        val acts = plan(mode, base, lmap, remoteHashed, local).toMutableList()
        if (!writable) acts.removeAll { it is Upload || it is DelRemote || it is MkRemote || it is MoveRemote }

        var rep = SyncReport(listed = fresh)
        val files = acts.filter { it is Upload || it is Download || it is KeepBoth }
        val total = files.size
        val totalBytes = files.sumOf { a ->
            when (a) {
                is Upload -> lmap[a.path]?.size ?: 0
                is Download -> remote[a.path]?.stamp?.size ?: 0
                else -> 0L
            }
        }
        var done = 0
        var bytes = 0L
        val made = mutableSetOf<String>()
        val errors = mutableListOf<String>()

        suspend fun ensureRemoteDir(dir: String) {
            if (dir.isEmpty() || dir in made || remote[dir]?.dir == true) return
            api.mkdir("$root/$dir", parents = true)
            var d = dir
            while (d.isNotEmpty()) { // every folder on the way now exists too
                made += d
                q.putBase(pair.id, d, 1, 0, 0, 0, 0, "")
                d = parentOf(d)
            }
        }

        fun log(kind: String, path: String, detail: String = "") {
            q.addLog(pair.id, now(), kind, path, detail)
        }

        // order: folders, moves, file transfers, then deletes (files before folders, deepest first)
        val order = acts.sortedWith(compareBy<Act>({
            when (it) {
                is MkRemote, is MkLocal -> 0
                is MoveRemote -> 1
                is Record, is Forget -> 2
                is KeepBoth, is Upload, is Download -> 3
                is DelRemote -> if (it.dir) 5 else 4
                is DelLocal -> if (it.dir) 5 else 4
            }
        }, { if (it is DelRemote && it.dir || it is DelLocal && it.dir) -it.path.length else it.path.length }))

        for (a in order) {
            currentCoroutineContext().ensureActive()
            val p = a.path
            try {
                when (a) {
                    is MkRemote -> ensureRemoteDir(p)
                    is MkLocal -> {
                        local.mkdirs(p)
                        q.putBase(pair.id, p, 1, 0, 0, 0, 0, "")
                    }
                    is MoveRemote -> {
                        ensureRemoteDir(parentOf(p))
                        api.move("$root/${a.from}", "$root/$p")
                        val b = base.getValue(a.from)
                        q.dropBase(pair.id, a.from)
                        q.putBase(pair.id, p, 0, lmap.getValue(p).size, lmap.getValue(p).mtime, b.r.size, b.r.mtime, b.hash)
                        log("moved", p, "from ${a.from}")
                        rep = rep.copy(moved = rep.moved + 1)
                    }
                    is Record -> q.putBase(pair.id, p, if (a.dir) 1 else 0, a.l.size, a.l.mtime, a.r.size, a.r.mtime, a.hash)
                    is Forget -> q.dropBase(pair.id, p)
                    is Upload -> {
                        progress(SyncProgress("Uploading", done, total, bytes, totalBytes, p))
                        val le = lmap.getValue(p)
                        ensureRemoteDir(parentOf(p))
                        val res = upload(pair, local, root, p, le, a.conflict, a.expect) { progress(SyncProgress("Uploading", done, total, bytes + it, totalBytes, p)) }
                        bytes += le.size
                        done++
                        if (res != null) {
                            q.putBase(pair.id, p, 0, le.size, le.mtime, res.size, res.mtime, res.sha256)
                            log("uploaded", p)
                            rep = rep.copy(uploaded = rep.uploaded + 1)
                        }
                    }
                    is Download -> {
                        progress(SyncProgress("Downloading", done, total, bytes, totalBytes, p))
                        val re = remote.getValue(p)
                        // don't overwrite a local file that changed since it was looked at
                        if (a.replace != null && local.stat(p)?.stamp != a.replace) throw IllegalStateException("changed on the phone during the sync; next time")
                        val got = download(root, p, re, local)
                        bytes += re.stamp.size
                        done++
                        q.putBase(pair.id, p, 0, got.size, got.mtime, re.stamp.size, re.stamp.mtime, re.hash)
                        log("downloaded", p)
                        rep = rep.copy(downloaded = rep.downloaded + 1)
                    }
                    is KeepBoth -> {
                        progress(SyncProgress("Keeping both versions", done, total, bytes, totalBytes, p))
                        val le = lmap.getValue(p)
                        val re = remote.getValue(p)
                        val copy = conflictName(p)
                        when (mode) {
                            SyncMode.BACKUP -> {
                                // keep Burrow's version; the phone's goes next to it
                                ensureRemoteDir(parentOf(p))
                                upload(pair, local, root, copy, le, "rename", null, srcPath = p) {}
                                q.putBase(pair.id, p, 0, le.size, le.mtime, re.stamp.size, re.stamp.mtime, re.hash)
                            }
                            SyncMode.TWO_WAY -> {
                                if (!local.rename(p, copy)) throw IllegalStateException("couldn't rename it on the phone")
                                val moved = local.stat(copy) ?: throw IllegalStateException("couldn't rename it on the phone")
                                ensureRemoteDir(parentOf(p))
                                val up = upload(pair, local, root, copy, moved, "rename", null) {}
                                if (up != null) q.putBase(pair.id, copy, 0, moved.size, moved.mtime, up.size, up.mtime, up.sha256)
                                val got = download(root, p, re, local)
                                q.putBase(pair.id, p, 0, got.size, got.mtime, re.stamp.size, re.stamp.mtime, re.hash)
                            }
                            SyncMode.DOWNLOAD -> {
                                if (!local.rename(p, copy)) throw IllegalStateException("couldn't rename it on the phone")
                                val got = download(root, p, re, local)
                                q.putBase(pair.id, p, 0, got.size, got.mtime, re.stamp.size, re.stamp.mtime, re.hash)
                            }
                        }
                        done++
                        log("conflict", p, "kept both: $copy")
                        rep = rep.copy(conflicts = rep.conflicts + 1)
                    }
                    is DelRemote -> {
                        if (a.dir) {
                            api.delete(listOf("$root/$p")).first().let { if (!it.ok && it.code != 404) throw IllegalStateException(it.error) }
                            q.dropBaseTree(pair.id, p, likeEscape(p) + "/%")
                        } else {
                            val r = api.deleteIfUnchanged("$root/$p", a.expect.size, a.expect.mtime)
                            if (!r.ok && r.code == 409) throw IllegalStateException("changed on Burrow in the meantime; next time")
                            if (!r.ok && r.code != 404) throw IllegalStateException(r.error)
                            q.dropBase(pair.id, p)
                        }
                        log("deleted on Burrow", p)
                        rep = rep.copy(deletedRemote = rep.deletedRemote + 1)
                    }
                    is DelLocal -> {
                        if (!a.dir && a.expect != null && local.stat(p)?.stamp != a.expect) throw IllegalStateException("changed on the phone in the meantime; next time")
                        if (local.delete(p) || local.stat(p) == null) {
                            if (a.dir) q.dropBaseTree(pair.id, p, likeEscape(p) + "/%") else q.dropBase(pair.id, p)
                            log("deleted on the phone", p)
                            rep = rep.copy(deletedLocal = rep.deletedLocal + 1)
                        }
                    }
                }
            } catch (e: UploadConflictException) {
                errors += "$p: changed on Burrow in the meantime; it will be sorted out next time"
            } catch (e: UnreachableException) {
                throw e
            } catch (e: ChannelException) {
                throw e
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                errors += "$p: ${e.message ?: e::class.simpleName}"
                log("error", p, e.message.orEmpty())
            }
        }
        q.trimLog(pair.id, pair.id)
        return rep.copy(errors = errors)
    }

    private suspend fun upload(pair: PairRow, local: LocalTree, root: String, path: String, le: LocalEntry, conflict: String, expect: Stamp?, srcPath: String = path, progress: (Long) -> Unit): UploadDone? {
        val pending = q.pendingOf(pair.id, path).executeAsOneOrNull()?.takeIf { it.size == le.size && it.mtime == le.mtime }
        val dir = parentOf(path)
        val done = uploader.upload(
            dir = if (dir.isEmpty()) root else "$root/$dir", name = nameOf(path), size = le.size, mtime = le.mtime,
            open = { local.open(srcPath) }, conflict = conflict, expect = expect, resumeId = pending?.upload_id,
            saveId = { id -> if (id == null) q.dropPending(pair.id, path) else q.putPending(pair.id, path, id, le.size, le.mtime) },
            progress = progress,
        )
        return if (done.skipped) null else done
    }

    private suspend fun download(root: String, path: String, re: Remote, local: LocalTree): LocalEntry =
        api.raw("$root/$path").use { r -> local.write(path, re.stamp.mtime) { sink -> sink.writeAll(r.body) } }

    private fun conflictName(path: String): String {
        val name = nameOf(path)
        val dot = name.lastIndexOf('.').takeIf { it > 0 } ?: name.length
        val n = name.substring(0, dot) + " (" + conflictTag() + ")" + name.substring(dot)
        val dir = parentOf(path)
        return if (dir.isEmpty()) n else "$dir/$n"
    }

    // ---- the decision table ----

    private fun plan(mode: SyncMode, base: Map<String, Base>, local: Map<String, LocalEntry>, remote: Map<String, Remote>, tree: LocalTree): List<Act> {
        val acts = mutableListOf<Act>()
        val paths = (base.keys + local.keys + remote.keys).toSortedSet()
        fun delta(b: Stamp?, now: Stamp?) = when {
            b == null && now == null -> Delta.ABSENT
            b == null -> Delta.NEW
            now == null -> Delta.GONE
            b == now -> Delta.SAME
            else -> Delta.CHANGED
        }
        for (p in paths) {
            val b = base[p]
            val l = local[p]
            val r = remote[p]
            val isDir = l?.dir ?: r?.dir ?: b?.dir ?: false
            if (isDir || (l?.dir == true) || (r?.dir == true)) {
                planDir(mode, p, b, l, r, local, remote, acts)
                continue
            }
            val ld = delta(b?.l, l?.stamp)
            val rd = delta(b?.r, r?.stamp)
            val same = { l != null && r != null && sameContent(l, r, tree) }
            when (mode) {
                SyncMode.TWO_WAY -> when {
                    ld == Delta.NEW && rd == Delta.ABSENT -> acts += Upload(p, "fail", null)
                    ld == Delta.ABSENT && rd == Delta.NEW -> acts += Download(p, null)
                    ld == Delta.NEW && rd == Delta.NEW -> acts += if (same()) Record(p, l!!.stamp, r!!.stamp, r.hash) else KeepBoth(p)
                    ld == Delta.SAME && rd == Delta.SAME -> Unit
                    ld == Delta.CHANGED && rd == Delta.SAME -> acts += Upload(p, "overwrite", b!!.r)
                    ld == Delta.SAME && rd == Delta.CHANGED -> acts += Download(p, l!!.stamp)
                    ld == Delta.CHANGED && rd == Delta.CHANGED -> acts += if (same()) Record(p, l!!.stamp, r!!.stamp, r.hash) else KeepBoth(p)
                    ld == Delta.GONE && rd == Delta.SAME -> acts += DelRemote(p, b!!.r, false)
                    ld == Delta.GONE && rd == Delta.CHANGED -> acts += Download(p, null)
                    ld == Delta.SAME && rd == Delta.GONE -> acts += DelLocal(p, l!!.stamp, false)
                    ld == Delta.CHANGED && rd == Delta.GONE -> acts += Upload(p, "fail", null)
                    ld == Delta.GONE && rd == Delta.GONE -> acts += Forget(p)
                }
                SyncMode.BACKUP -> when {
                    ld == Delta.NEW && rd == Delta.ABSENT -> acts += Upload(p, "fail", null)
                    ld == Delta.NEW && rd == Delta.NEW -> acts += if (same()) Record(p, l!!.stamp, r!!.stamp, r.hash) else KeepBoth(p)
                    ld == Delta.SAME && rd == Delta.SAME -> Unit
                    ld == Delta.CHANGED && rd == Delta.SAME -> acts += Upload(p, "overwrite", b!!.r)
                    ld == Delta.SAME && rd == Delta.CHANGED -> acts += Record(p, l!!.stamp, r!!.stamp, r.hash) // someone edited it on Burrow
                    ld == Delta.CHANGED && rd == Delta.CHANGED -> acts += if (same()) Record(p, l!!.stamp, r!!.stamp, r.hash) else KeepBoth(p)
                    (ld == Delta.SAME || ld == Delta.CHANGED) && rd == Delta.GONE -> acts += Upload(p, "fail", null)
                    ld == Delta.GONE -> acts += Forget(p) // deleting on the phone deletes nothing on Burrow
                }
                SyncMode.DOWNLOAD -> when {
                    ld == Delta.ABSENT && rd == Delta.NEW -> acts += Download(p, null)
                    ld == Delta.NEW && rd == Delta.NEW -> acts += if (same()) Record(p, l!!.stamp, r!!.stamp, r.hash) else KeepBoth(p)
                    ld == Delta.SAME && rd == Delta.CHANGED -> acts += Download(p, l!!.stamp)
                    ld == Delta.CHANGED && rd == Delta.CHANGED -> acts += if (same()) Record(p, l!!.stamp, r!!.stamp, r.hash) else KeepBoth(p)
                    ld == Delta.GONE && (rd == Delta.SAME || rd == Delta.CHANGED) -> acts += Download(p, null)
                    ld == Delta.SAME && rd == Delta.GONE -> acts += DelLocal(p, l!!.stamp, false)
                    (ld == Delta.CHANGED || ld == Delta.GONE) && rd == Delta.GONE -> acts += Forget(p)
                    else -> Unit // a phone-only file, or an edit on the phone: left alone
                }
            }
        }
        if (mode != SyncMode.DOWNLOAD) detectMoves(base, local, remote, acts)
        return acts
    }

    private fun planDir(mode: SyncMode, p: String, b: Base?, l: LocalEntry?, r: Remote?, local: Map<String, LocalEntry>, remote: Map<String, Remote>, acts: MutableList<Act>) {
        val zero = Stamp(0, 0)
        when {
            l != null && r != null -> if (b == null) acts += Record(p, zero, zero, "", dir = true)
            l != null && r == null -> when {
                b == null && mode != SyncMode.DOWNLOAD -> acts += MkRemote(p)
                b != null && mode == SyncMode.TWO_WAY && emptyAfter(p, local, acts) -> acts += DelLocal(p, null, true)
                b != null && mode == SyncMode.DOWNLOAD && emptyAfter(p, local, acts) -> acts += DelLocal(p, null, true)
                b != null && mode == SyncMode.BACKUP -> acts += MkRemote(p)
            }
            l == null && r != null -> when {
                b == null && mode != SyncMode.BACKUP -> acts += MkLocal(p)
                b != null && mode == SyncMode.TWO_WAY && remoteEmptyAfter(p, remote, acts) -> acts += DelRemote(p, zero, true)
                b != null && mode == SyncMode.DOWNLOAD -> acts += MkLocal(p)
                b != null && mode == SyncMode.BACKUP -> acts += Forget(p)
            }
            else -> if (b != null) acts += Forget(p)
        }
    }

    /** Will local folder p be empty once the planned local deletes are done? */
    private fun emptyAfter(p: String, local: Map<String, LocalEntry>, acts: List<Act>): Boolean {
        val gone = acts.filterIsInstance<DelLocal>().map { it.path }.toSet()
        return local.keys.none { it.startsWith("$p/") && it !in gone && local[it]?.dir == false }
    }

    private fun remoteEmptyAfter(p: String, remote: Map<String, Remote>, acts: List<Act>): Boolean {
        val gone = acts.filterIsInstance<DelRemote>().map { it.path }.toSet()
        return remote.keys.none { it.startsWith("$p/") && it !in gone && remote[it]?.dir == false }
    }

    /**
     * A file that disappeared from one path on the phone and appeared, unchanged,
     * at another is a move: move it on Burrow instead of uploading it again.
     */
    private fun detectMoves(base: Map<String, Base>, local: Map<String, LocalEntry>, remote: Map<String, Remote>, acts: MutableList<Act>) {
        val gone = base.filter { (p, b) -> !b.dir && local[p] == null && remote[p]?.stamp == b.r }
        if (gone.isEmpty()) return
        val news = acts.filterIsInstance<Upload>().filter { it.expect == null && base[it.path] == null && remote[it.path] == null }
        val byStamp = gone.entries.groupBy { it.value.l }
        val used = mutableSetOf<String>()
        for (u in news) {
            val le = local[u.path] ?: continue
            val cands = byStamp[le.stamp]?.filter { it.key !in used } ?: continue
            if (cands.size != 1) continue
            val from = cands.single().key
            used += from
            acts.remove(u)
            acts.removeAll { (it is DelRemote && it.path == from) || (it is Forget && it.path == from) }
            acts += MoveRemote(u.path, from)
        }
    }

    /**
     * Same file on both sides? Same size and (nearly) the same time is taken as
     * the same, as rsync does; otherwise the contents are compared by SHA-256.
     */
    private fun sameContent(l: LocalEntry, r: Remote, tree: LocalTree): Boolean {
        if (l.size != r.stamp.size) return false
        if (l.size == 0L || closeTimes(l.mtime, r.stamp.mtime)) return true
        val rh = r.hash.ifEmpty { return false } // fetched before planning where it matters
        return Uploader.sha256 { tree.open(l.path) } == rh
    }

    /** Syncs one pair. Progress is reported as it goes; the result says what happened. */
    private inline fun missingAsChanged(check: () -> Boolean): Boolean = try {
        check()
    } catch (e: ApiException) {
        if (e.status != 404) throw e
        true
    }

    suspend fun sync(pair: PairRow, local: LocalTree, progress: (SyncProgress) -> Unit = {}): SyncReport {
        try {
            val rep = run(pair, local, progress)
            q.finished(now(), rep.errors.firstOrNull().orEmpty(), pair.id)
            return rep
        } catch (e: Exception) {
            q.finished(pair.last_sync, e.message ?: "sync failed", pair.id)
            throw e
        }
    }

    companion object {
        /** Burrow's own default hidden names, used until the server's list is known. */
        val DEFAULT_EXCLUDES = listOf(".burrow-*", ".zfs", ".hist", "@eaDir", "#recycle", "#snapshot", ".Trash-*", "\$RECYCLE.BIN", "System Volume Information", ".DS_Store", "._*", "Thumbs.db", "desktop.ini", ".ix-apps", "lost+found")

        /** Modification times this close are the same file's (filesystems round differently). */
        fun closeTimes(a: Long, b: Long) = kotlin.math.abs(a - b) <= 1000

        fun parentOf(p: String) = p.substringBeforeLast('/', "")
        fun nameOf(p: String) = p.substringAfterLast('/')

        /** Burrow's hidden-name patterns, matched case-insensitively against every path segment. */
        fun excluded(path: String, patterns: List<String>): Boolean =
            patterns.isNotEmpty() && path.split('/').any { seg -> patterns.any { glob(it.lowercase(), seg.lowercase()) } }

        internal fun glob(p: String, s: String): Boolean {
            var pi = 0
            var si = 0
            var star = -1
            var mark = 0
            while (si < s.length) {
                when {
                    pi < p.length && (p[pi] == '?' || p[pi] == s[si]) -> { pi++; si++ }
                    pi < p.length && p[pi] == '*' -> { star = pi++; mark = si }
                    star >= 0 -> { pi = star + 1; si = ++mark }
                    else -> return false
                }
            }
            while (pi < p.length && p[pi] == '*') pi++
            return pi == p.length
        }

        internal fun likeEscape(s: String) = s.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
    }
}
