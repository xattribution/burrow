package io.github.xattribution.burrow.core

import okio.BufferedSink
import okio.Source
import okio.buffer
import okio.sink
import okio.source
import java.io.File
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.StandardCopyOption

/** A LocalTree on a plain directory (app storage on Android, temp folders in tests). */
class FileTree(private val root: File) : LocalTree {
    private fun f(path: String) = File(root, path)

    private fun entry(rel: String, file: File) = LocalEntry(rel, file.isDirectory, if (file.isDirectory) 0 else file.length(), file.lastModified())

    override suspend fun list(): List<LocalEntry> {
        val out = mutableListOf<LocalEntry>()
        fun walk(dir: File, prefix: String) {
            for (c in dir.listFiles().orEmpty().sortedBy { it.name }) {
                if (Files.isSymbolicLink(c.toPath())) continue
                val rel = if (prefix.isEmpty()) c.name else "$prefix/${c.name}"
                out += entry(rel, c)
                if (c.isDirectory) walk(c, rel)
            }
        }
        walk(root, "")
        return out
    }

    override suspend fun stat(path: String): LocalEntry? = f(path).takeIf { Files.exists(it.toPath(), LinkOption.NOFOLLOW_LINKS) }?.let { entry(path, it) }

    override fun open(path: String): Source = f(path).source()

    override suspend fun write(path: String, mtime: Long, body: (BufferedSink) -> Unit): LocalEntry {
        val dst = f(path)
        dst.parentFile.mkdirs()
        val tmp = File(dst.parentFile, ".burrow-dl-" + dst.name)
        try {
            tmp.sink().buffer().use(body)
            if (mtime > 0) tmp.setLastModified(mtime)
            Files.move(tmp.toPath(), dst.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
        } finally {
            tmp.delete()
        }
        return entry(path, dst)
    }

    override suspend fun mkdirs(path: String) {
        f(path).mkdirs()
    }

    override suspend fun delete(path: String): Boolean = f(path).delete()

    override suspend fun rename(from: String, to: String): Boolean {
        val d = f(to)
        if (d.exists()) return false
        d.parentFile.mkdirs()
        return f(from).renameTo(d)
    }
}
