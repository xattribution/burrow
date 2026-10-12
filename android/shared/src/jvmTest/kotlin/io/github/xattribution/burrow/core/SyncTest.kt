package io.github.xattribution.burrow.core

import app.cash.sqldelight.driver.jdbc.sqlite.JdbcSqliteDriver
import io.github.xattribution.burrow.core.db.BurrowDb
import kotlinx.coroutines.runBlocking
import org.junit.AfterClass
import org.junit.BeforeClass
import java.io.File
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** The sync engine against the real server, on temporary folders. */
class SyncTest {
    companion object {
        lateinit var server: TestServer
        lateinit var api: BurrowApi

        @BeforeClass @JvmStatic
        fun up() = runBlocking {
            server = TestServer(listOf("DOCS", "PHOTOS", "SYNC"))
            val link = PairingLink.parse(server.pairingLink())
            val tr = OkHttpTransport(BurrowTls.client({ setOfNotNull(link.pin) }))
            val res = BurrowChannel.pair(tr, link, DeviceInfo("Sync Test", "android"))
            api = BurrowApi(BurrowChannel(tr, StaticRoutes(listOf(server.lan)), res.keys))
        }

        @AfterClass @JvmStatic
        fun down() = server.close()
    }

    private var clock = 1_800_000_000L

    private class Fixture(val db: BurrowDb, val engine: SyncEngine, val dir: File, val tree: FileTree, val pairId: Long, val remote: File)

    private fun fixture(mode: SyncMode, remote: String, create: Boolean = true): Fixture {
        val driver = JdbcSqliteDriver(JdbcSqliteDriver.IN_MEMORY)
        BurrowDb.Schema.create(driver)
        val db = BurrowDb(driver)
        val dir = Files.createTempDirectory("phone").toFile()
        if (create) File(server.lib, remote).mkdirs()
        db.pairQueries.insert("t", dir.path, "phone", remote, mode.id, 0, 0, 1)
        val id = db.pairQueries.all().executeAsList().last().id
        return Fixture(db, SyncEngine(api, db, now = { clock++ }, conflictTag = { "conflict Sync Test" }), dir, FileTree(dir), id, File(server.lib, remote))
    }

    private fun Fixture.sync(): SyncReport = runBlocking {
        engine.sync(db.pairQueries.byId(pairId).executeAsOne(), tree).also { assertEquals(emptyList(), it.errors, "sync errors") }
    }

    /** Burrow notices changes made straight on disk within a couple of seconds (inotify, then a short rescan). */
    private fun settle() = Thread.sleep(2600)

    private fun write(f: File, text: String, mtime: Long? = null) {
        f.parentFile.mkdirs()
        f.writeText(text)
        if (mtime != null) f.setLastModified(mtime)
    }

    private fun names(d: File): Set<String> = d.walkTopDown().filter { it != d && !it.name.startsWith(".burrow") && it.path.split(File.separator).none { s -> s.startsWith(".burrow") } }
        .map { it.relativeTo(d).invariantSeparatorsPath + if (it.isDirectory) "/" else "" }.toSet()

    @Test
    fun twoWay() {
        val fx = fixture(SyncMode.TWO_WAY, "SYNC/both")
        write(File(fx.dir, "phone.txt"), "from phone")
        write(File(fx.dir, "trip/day1.txt"), "beach")
        File(fx.dir, "empty-on-phone").mkdirs()
        write(File(fx.remote, "nas.txt"), "from nas")
        File(fx.remote, "empty-on-nas").mkdirs()
        val r1 = fx.sync()
        assertEquals(2, r1.uploaded)
        assertEquals(1, r1.downloaded)
        assertEquals(names(fx.dir), names(fx.remote))
        assertEquals("from nas", File(fx.dir, "nas.txt").readText())
        assertEquals("beach", File(fx.remote, "trip/day1.txt").readText())
        assertEquals(File(fx.dir, "trip/day1.txt").lastModified(), File(fx.remote, "trip/day1.txt").lastModified())

        // nothing changed: nothing moves; once Burrow's change log has nothing new, its side isn't even listed
        assertEquals(0, fx.sync().changes)
        val r2 = fx.sync()
        assertEquals(0, r2.changes)
        assertFalse(r2.listed)

        // an edit on each side travels to the other
        write(File(fx.dir, "phone.txt"), "phone v2")
        Thread.sleep(1100)
        write(File(fx.remote, "nas.txt"), "nas v2")
        settle()
        val r3 = fx.sync()
        assertEquals(1, r3.uploaded, r3.toString())
        assertEquals(1, r3.downloaded, r3.toString())
        assertEquals("phone v2", File(fx.remote, "phone.txt").readText())
        assertEquals("nas v2", File(fx.dir, "nas.txt").readText())
        // the replaced version waits in Burrow's trash
        assertTrue(runBlocking { api.trash() }.any { it.path == "SYNC/both/phone.txt" })

        // the same file changed on both sides: both versions are kept, everywhere
        write(File(fx.dir, "nas.txt"), "phone edit")
        write(File(fx.remote, "nas.txt"), "nas edit!")
        settle()
        val r4 = fx.sync()
        assertEquals(1, r4.conflicts, r4.toString())
        assertEquals("nas edit!", File(fx.dir, "nas.txt").readText())
        assertEquals("phone edit", File(fx.dir, "nas (conflict Sync Test).txt").readText())
        assertEquals("phone edit", File(fx.remote, "nas (conflict Sync Test).txt").readText())

        // deletes travel both ways (Burrow keeps its copy in the trash)
        File(fx.dir, "phone.txt").delete()
        File(fx.remote, "trip/day1.txt").delete()
        settle()
        val r5 = fx.sync()
        assertEquals(1, r5.deletedRemote, r5.toString())
        assertTrue(r5.deletedLocal >= 1, r5.toString())
        assertFalse(File(fx.remote, "phone.txt").exists())
        assertFalse(File(fx.dir, "trip/day1.txt").exists())
        assertEquals(names(fx.dir), names(fx.remote))

        // a move on the phone is a move on Burrow, not a second upload
        write(File(fx.dir, "a/big.bin"), "x".repeat(50_000))
        fx.sync()
        val before = runBlocking { api.trash() }.size
        File(fx.dir, "b").mkdirs()
        File(fx.dir, "a/big.bin").renameTo(File(fx.dir, "b/big.bin"))
        val r6 = fx.sync()
        assertEquals(1, r6.moved, r6.toString())
        assertEquals(0, r6.uploaded, r6.toString())
        assertTrue(File(fx.remote, "b/big.bin").exists())
        assertFalse(File(fx.remote, "a/big.bin").exists())
        assertEquals(before, runBlocking { api.trash() }.size)
    }

    @Test
    fun backup() {
        val fx = fixture(SyncMode.BACKUP, "PHOTOS/phone")
        write(File(fx.dir, "DCIM/Camera/IMG_1.jpg"), "one")
        write(File(fx.dir, "DCIM/Camera/IMG_2.jpg"), "two")
        write(File(fx.dir, "Thumbs.db"), "hidden by Burrow")
        write(File(fx.remote, "only-on-nas.txt"), "stays")
        assertEquals(2, fx.sync().uploaded)
        assertTrue(File(fx.remote, "DCIM/Camera/IMG_2.jpg").exists())
        assertFalse(File(fx.remote, "Thumbs.db").exists())
        assertFalse(File(fx.dir, "only-on-nas.txt").exists()) // nothing comes down
        // deleting on the phone deletes nothing on Burrow
        File(fx.dir, "DCIM/Camera/IMG_1.jpg").delete()
        assertEquals(0, fx.sync().changes)
        assertTrue(File(fx.remote, "DCIM/Camera/IMG_1.jpg").exists())
        // what is still on the phone but gone from Burrow goes up again
        File(fx.remote, "DCIM/Camera/IMG_2.jpg").delete()
        settle()
        assertEquals(1, fx.sync().uploaded)
        assertTrue(File(fx.remote, "DCIM/Camera/IMG_2.jpg").exists())
        // a file already on Burrow with the same contents isn't sent again
        write(File(fx.remote, "DCIM/Camera/IMG_3.jpg"), "three", 1_600_000_000_000)
        write(File(fx.dir, "DCIM/Camera/IMG_3.jpg"), "three", 1_700_000_000_000)
        settle()
        assertEquals(0, fx.sync().uploaded)
        // a different file of the same name: Burrow keeps its own, the phone's goes next to it
        write(File(fx.remote, "DCIM/Camera/IMG_4.jpg"), "nas four", 1_600_000_000_000)
        write(File(fx.dir, "DCIM/Camera/IMG_4.jpg"), "phone 4!", 1_700_000_000_000)
        settle()
        assertEquals(1, fx.sync().conflicts)
        assertEquals("nas four", File(fx.remote, "DCIM/Camera/IMG_4.jpg").readText())
        assertEquals("phone 4!", File(fx.remote, "DCIM/Camera/IMG_4 (conflict Sync Test).jpg").readText())
    }

    @Test
    fun aMissingFolderOnBurrow() {
        // the first sync makes it
        val fx = fixture(SyncMode.TWO_WAY, "SYNC/new/deep", create = false)
        write(File(fx.dir, "a.txt"), "a")
        assertEquals(1, fx.sync().uploaded)
        assertEquals("a", File(fx.remote, "a.txt").readText())
        // once synced, a folder that disappears is not "delete everything on the phone"
        fx.remote.deleteRecursively()
        settle()
        val e = assertFailsWith<ApiException> { runBlocking { fx.engine.sync(fx.db.pairQueries.byId(fx.pairId).executeAsOne(), fx.tree) } }
        assertEquals(404, e.status)
        assertEquals("a", File(fx.dir, "a.txt").readText())
        assertFalse(fx.remote.exists())
        // a backup puts it back
        val bx = fixture(SyncMode.BACKUP, "PHOTOS/gone", create = false)
        write(File(bx.dir, "DCIM/IMG_1.jpg"), "one")
        assertEquals(1, bx.sync().uploaded)
        bx.remote.deleteRecursively()
        settle()
        assertEquals(1, bx.sync().uploaded)
        assertEquals("one", File(bx.remote, "DCIM/IMG_1.jpg").readText())
    }

    @Test
    fun downloadToPhone() {
        val fx = fixture(SyncMode.DOWNLOAD, "DOCS/offline")
        write(File(fx.remote, "manual.pdf"), "pdf")
        write(File(fx.remote, "sub/notes.txt"), "notes")
        assertEquals(2, fx.sync().downloaded)
        assertEquals("notes", File(fx.dir, "sub/notes.txt").readText())
        // phone-only files and phone edits never go up
        write(File(fx.dir, "mine.txt"), "mine")
        write(File(fx.dir, "manual.pdf"), "scribbled")
        val r = fx.sync()
        assertEquals(0, r.uploaded)
        assertFalse(File(fx.remote, "mine.txt").exists())
        assertEquals("pdf", File(fx.remote, "manual.pdf").readText())
        // when Burrow's copy changes too, the phone keeps its edit as a conflict copy
        Thread.sleep(1100)
        write(File(fx.remote, "manual.pdf"), "pdf v2")
        settle()
        fx.sync()
        assertEquals("pdf v2", File(fx.dir, "manual.pdf").readText())
        assertEquals("scribbled", File(fx.dir, "manual (conflict Sync Test).pdf").readText())
        // deleted on Burrow: deleted on the phone
        File(fx.remote, "sub/notes.txt").delete()
        settle()
        fx.sync()
        assertFalse(File(fx.dir, "sub/notes.txt").exists())
    }

    @Test
    fun anInterruptedUploadResumes() = runBlocking {
        val dir = Files.createTempDirectory("big").toFile()
        val data = ByteArray(20 shl 20) { (it % 251).toByte() }
        File(dir, "video.mp4").writeBytes(data)
        val tree = FileTree(dir)
        var savedId: String? = null
        var sent = 0L
        // first attempt: stop after the first chunk
        val first = runCatching {
            Uploader(api).upload("PHOTOS", "video.mp4", data.size.toLong(), 1_700_000_000_000, { tree.open("video.mp4") },
                saveId = { savedId = it }, progress = { if (it > 0) { sent = it; throw IllegalStateException("network dropped") } })
        }
        assertTrue(first.isFailure)
        assertTrue(savedId != null && sent == (8L shl 20), "id=$savedId sent=$sent")
        var resumedFrom = -1L
        val done = Uploader(api).upload("PHOTOS", "video.mp4", data.size.toLong(), 1_700_000_000_000, { tree.open("video.mp4") },
            resumeId = savedId, saveId = { savedId = it }, progress = { if (resumedFrom < 0) resumedFrom = it })
        assertEquals(8L shl 20, resumedFrom)
        assertEquals(null, savedId)
        assertEquals(data.size.toLong(), done.size)
        assertTrue(File(server.lib, "PHOTOS/video.mp4").readBytes().contentEquals(data))
    }

    @Test
    fun globMatchesLikeTheServer() {
        assertTrue(SyncEngine.excluded("a/._foo/b", listOf("._*")))
        assertTrue(SyncEngine.excluded("Thumbs.DB", listOf("thumbs.db")))
        assertTrue(SyncEngine.excluded("x/.Trash-1000/y", listOf(".Trash-*")))
        assertFalse(SyncEngine.excluded("photos/img.jpg", SyncEngine.DEFAULT_EXCLUDES))
        assertTrue(SyncEngine.excluded("a/.burrow-part-x", SyncEngine.DEFAULT_EXCLUDES))
    }
}
