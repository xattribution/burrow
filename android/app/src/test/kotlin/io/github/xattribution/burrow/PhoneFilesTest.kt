package io.github.xattribution.burrow

import android.os.Environment
import androidx.test.core.app.ApplicationProvider
import io.github.xattribution.burrow.data.NoPhoneAccessException
import io.github.xattribution.burrow.data.PhoneFs
import io.github.xattribution.burrow.data.PhoneTree
import io.github.xattribution.burrow.data.Place
import io.github.xattribution.burrow.data.localTreeFor
import kotlinx.coroutines.runBlocking
import okio.Buffer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.io.File
import java.nio.file.Files

/** The phone's own storage as a place, and any phone folder as the local side of a sync. */
@RunWith(RobolectricTestRunner::class)
class PhoneFilesTest {
    private val root: File = Files.createTempDirectory("phone").toFile()
    private val fs = PhoneFs(Place.PHONE, root) { true }

    @Test
    fun browseAndChangeFiles() = runBlocking {
        File(root, "Download").mkdirs()
        File(root, "Download/report.pdf").writeText("pdf body")
        File(root, "Download/.pending").writeText("hidden")
        File(root, "Notes.txt").writeText("hello")

        // folders first, hidden files left out, the top of the volume writable
        val top = fs.ls("")
        assertEquals(listOf("Download", "Notes.txt"), top.entries.map { it.name })
        assertTrue(top.canWrite)
        assertEquals(listOf("report.pdf"), fs.ls("Download").entries.map { it.name })
        assertEquals("doc", fs.stat("Download/report.pdf").kind)

        // reads from an offset, as a player seeking does
        fs.open("Download/report.pdf", from = 4).use { assertEquals("body", it.source.readUtf8()) }

        // a copy never replaces: the second gets "(2)"
        assertEquals("Download/report (2).pdf", fs.put("Download", "report.pdf", 3, 1_700_000_000_000, open = { Buffer().writeUtf8("two") }))
        assertEquals(1_700_000_000_000, File(root, "Download/report (2).pdf").lastModified())
        assertTrue("a short copy is refused and removed", runCatching { fs.put("Download", "short.txt", 10, 0, open = { Buffer().writeUtf8("abc") }) }.isFailure)
        assertTrue(!File(root, "Download/short.txt").exists())

        fs.mkdir("Old")
        assertTrue("an existing folder isn't made again", runCatching { fs.mkdir("Old") }.isFailure)
        assertEquals("Old/report.pdf", fs.move("Download/report.pdf", "Old/report.pdf"))
        assertTrue("a move doesn't replace", runCatching { fs.move("Download/report (2).pdf", "Old/report.pdf") }.isFailure)

        // search by name, every word
        assertEquals(listOf("Old/report.pdf", "Download/report (2).pdf").sorted(), fs.find("", "report").map { it.path }.sorted())
        assertEquals(listOf("Download/report (2).pdf"), fs.find("", "report 2").map { it.path })
        assertTrue("hidden files aren't found", fs.find("", "pending").isEmpty())

        fs.delete("Old")
        assertTrue(!File(root, "Old").exists())
        assertTrue("paths can't climb out of the volume", runCatching { fs.ls("../..") }.isFailure)
        assertTrue("the volume itself isn't deleted", runCatching { fs.delete("") }.isFailure)
    }

    @Test
    fun noAccessSaysSo() = runBlocking {
        val locked = PhoneFs(Place.PHONE, root) { false }
        assertTrue(runCatching { locked.ls("") }.exceptionOrNull() is NoPhoneAccessException)
    }

    @Test
    fun thisPhoneIsAPlace() {
        val ctx = ApplicationProvider.getApplicationContext<TestApp>()
        val places = ctx.graph.places.list.value
        assertEquals(Place.HOME, places[0].id)
        val phone = places[1] as Place.Phone
        assertEquals("This phone", phone.name)
        assertEquals(Environment.getExternalStorageDirectory(), phone.root)
    }

    @Test
    fun anyPhoneFolderSyncs() = runBlocking {
        val ctx = ApplicationProvider.getApplicationContext<TestApp>()
        File(root, "Download/keep.txt").apply { parentFile!!.mkdirs(); writeText("keep") }
        File(root, "Download/.thumbs/x.jpg").apply { parentFile!!.mkdirs(); writeText("x") }
        File(root, "Android/data/com.example/cache.bin").apply { parentFile!!.mkdirs(); writeText("app's own") }
        var access = true

        // a sync pair's phone side is a file: address, which Android's own picker never gives
        assertTrue(localTreeFor(ctx, android.net.Uri.fromFile(File(Environment.getExternalStorageDirectory(), "Download")).toString()) is PhoneTree)

        // Downloads, which Android's folder picker refuses to hand out
        val downloads = PhoneTree(File(root, "Download"), volumeTop = false) { access }
        assertEquals(listOf("keep.txt"), downloads.list().map { it.path })

        // the whole storage: apps' private folders and hidden files stay out
        val all = PhoneTree(root, volumeTop = true) { access }.list().map { it.path }
        assertTrue(all.toString(), "Download/keep.txt" in all && all.none { it.startsWith("Android") || it.contains("/.") })

        // without access, sync says why instead of seeing an empty folder
        access = false
        assertTrue(runCatching { downloads.list() }.exceptionOrNull()?.message.orEmpty().contains("access to all files"))
    }
}
