package io.github.xattribution.burrow

import android.content.Intent
import android.net.Uri
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.printToLog
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.longClick
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import com.github.takahirom.roborazzi.captureRoboImage
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import io.github.xattribution.burrow.ui.MainActivity
import io.github.xattribution.burrow.data.RemoteFileProvider
import io.github.xattribution.burrow.ui.files.DragDrop
import org.junit.AfterClass
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import kotlinx.coroutines.runBlocking
import org.junit.BeforeClass
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.GraphicsMode
import java.io.File

/**
 * The real app against a real Burrow server, at the window sizes it must handle:
 * a phone, a foldable's narrow cover screen, a foldable opened up, and a tablet.
 * Screenshots land in app/build/outputs/roborazzi.
 */
@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class UiTest {
    companion object {
        lateinit var server: TestServer

        @BeforeClass @JvmStatic
        fun up() {
            server = TestServer()
            File(server.lib, "PHOTOS/2024").mkdirs()
            File(server.lib, "DOCS/Manuals").mkdirs()
            File(server.lib, "DOCS/Manuals/printer.pdf").writeText("%PDF-1.4 not really")
            File(server.lib, "DOCS/recipes.md").writeText("# Pancakes\n\n2 eggs, 1 cup flour, milk.")
        }

        @AfterClass @JvmStatic
        fun down() = server.close()

        const val PHONE = "w411dp-h914dp-port-xxhdpi"
        const val COVER = "w360dp-h748dp-port-xxhdpi" // a foldable's outer screen
        const val UNFOLDED = "w841dp-h701dp-land-xhdpi" // the inner screen, opened up
        const val TABLET = "w1280dp-h800dp-land-mdpi"
    }

    @get:Rule
    val compose = createEmptyComposeRule()

    private fun shot(name: String) = compose.onRoot().captureRoboImage("build/outputs/roborazzi/$name.png")

    private var step = 0

    private fun waitFor(text: String, ms: Long = 30_000) {
        try {
            compose.waitUntil(ms) { compose.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty() }
        } catch (e: Throwable) {
            runCatching { shot("fail-${javaClass.simpleName}-${step++}") }
            runCatching { compose.onRoot().printToLog("UiTest") }
            val texts = runCatching {
                compose.onAllNodes(androidx.compose.ui.test.isRoot()).fetchSemanticsNodes().flatMap { root -> collect(root) }
            }.getOrDefault(emptyList())
            val log = runCatching { File(server.dir, "server.log").readLines().takeLast(15).joinToString("\n") }.getOrDefault("")
            throw AssertionError("waiting for \"$text\"; on screen: $texts\nserver log:\n$log", e)
        }
    }

    private fun collect(n: androidx.compose.ui.semantics.SemanticsNode): List<String> =
        n.config.getOrElseNullable(androidx.compose.ui.semantics.SemanticsProperties.Text) { null }.orEmpty().map { it.text } + n.children.flatMap { collect(it) }

    /** Opens the app from a pairing link (as scanning the QR in another app would) and pairs. */
    private fun pairAt(qualifiers: String, shotPrefix: String? = null): ActivityScenario<MainActivity> {
        RuntimeEnvironment.setQualifiers(qualifiers)
        val ctx = ApplicationProvider.getApplicationContext<android.content.Context>()
        val sc = ActivityScenario.launch<MainActivity>(Intent(Intent.ACTION_VIEW, Uri.parse(server.pairingLink()), ctx, MainActivity::class.java))
        waitFor("This phone will reach your Burrow at")
        if (shotPrefix != null) shot("$shotPrefix-pair")
        compose.onNodeWithText("Pair").performClick()
        waitFor("DOCS")
        return sc
    }

    @Test
    fun phone() {
        pairAt(PHONE, "phone")
        shot("phone-files")
        compose.onNodeWithText("DOCS").performClick()
        waitFor("recipes.md")
        shot("phone-folder")
        compose.onNodeWithText("recipes.md").performClick()
        waitFor("Pancakes")
        shot("phone-detail")
        // one pane at a time on a phone: the folder list is gone while the file shows
        assertTrue(compose.onAllNodesWithText("notes.txt").fetchSemanticsNodes().isEmpty())
        // make a link from the file
        compose.onNodeWithText("Share link").performClick()
        waitFor("Create link")
        shot("phone-new-link")
        compose.onNodeWithText("Create link").performClick()
        waitFor("Link ready")
        shot("phone-link-ready")
        assertTrue(server.web("GET", "/api/shares").contains("DOCS/recipes.md"))
    }

    @Test
    fun phoneDarkIsTrueBlack() {
        pairAt("w411dp-h914dp-port-night-xxhdpi")
        compose.onNodeWithText("DOCS").performClick()
        waitFor("recipes.md")
        shot("phone-dark-folder")
        // OLED: the screen behind the list is pixels-off black
        val img = compose.onRoot().captureToImage().asAndroidBitmap()
        assertEquals(android.graphics.Color.BLACK, img.getPixel(img.width / 2, img.height * 3 / 4))
    }

    @Test
    fun dragOutStreamsFromTheNas() {
        pairAt(PHONE)
        val ctx = ApplicationProvider.getApplicationContext<android.content.Context>()
        val body = File(server.lib, "DOCS/recipes.md").readBytes()
        val e = io.github.xattribution.burrow.core.Entry(path = "DOCS/recipes.md", name = "recipes.md", size = body.size.toLong())
        // the address another app gets, and what it learns from it without a download
        val uri = RemoteFileProvider.uriFor(ctx, e)
        val f = RemoteFileProvider.parse(uri)!!
        assertEquals(e.path, f.path)
        val provider = org.robolectric.Robolectric.buildContentProvider(RemoteFileProvider::class.java).create(RemoteFileProvider.authority(ctx)).get()
        provider.query(uri, null, null, null, null).use { c ->
            assertTrue(c.moveToFirst())
            assertEquals("recipes.md", c.getString(c.getColumnIndexOrThrow(android.provider.OpenableColumns.DISPLAY_NAME)))
            assertEquals(body.size.toLong(), c.getLong(c.getColumnIndexOrThrow(android.provider.OpenableColumns.SIZE)))
        }
        // reading streams from Burrow: in order, then a jump back, then past the end
        assertEquals("home", f.place)
        val r = RemoteFileProvider.Reader(ctx.graph.places.fs(f.place), f)
        val buf = ByteArray(8)
        assertEquals(8, r.onRead(0, 8, buf)); assertEquals(String(body, 0, 8), String(buf))
        assertEquals(8, r.onRead(8, 8, buf)); assertEquals(String(body, 8, 8), String(buf))
        assertEquals(6, r.onRead(2, 6, buf)); assertEquals(String(body, 2, 6), String(buf, 0, 6))
        assertEquals(0, r.onRead(body.size.toLong(), 8, buf))
        r.onRelease()
        // drops that would do nothing or loop are refused
        assertTrue(DragDrop.canMove("DOCS/recipes.md", "DOCS/Manuals"))
        assertTrue(!DragDrop.canMove("DOCS/recipes.md", "DOCS"))
        assertTrue(!DragDrop.canMove("DOCS/Manuals", "DOCS/Manuals"))
        assertTrue(!DragDrop.canMove("DOCS/Manuals", "DOCS/Manuals/old"))
    }

    /** Places added on the phone: another Burrow and (when a test SMB server runs) an SMB share. */
    @Test
    fun phoneOnlyConnections() {
        pairAt(PHONE)
        val ctx = ApplicationProvider.getApplicationContext<android.content.Context>()
        val g = ctx.graph
        TestServer(listOf("DOCS", "SHARED")).use { peer ->
            File(peer.lib, "SHARED/Trip/beach.txt").apply { parentFile!!.mkdirs(); writeText("sand and sun") }
            val alex = runBlocking { g.places.pairPeer(io.github.xattribution.burrow.core.PairingLink.parse(peer.pairingLink()), "Alex's Burrow", "Test phone") }
            // its keys are its own, and this phone's Burrow never hears of it
            assertTrue(alex.store.current!!.keys.deviceId.contentEquals(g.device.current!!.keys.deviceId).not())
            assertTrue(!server.web("GET", "/api/remotes").contains("Alex"))

            // browse it from the place switcher
            switchPlace()
            compose.onNodeWithText("Alex's Burrow").performClick()
            waitFor("SHARED")
            compose.onNodeWithText("SHARED").performClick()
            waitFor("Trip")
            shot("phone-place-peer")

            // copy a folder from it to this phone's Burrow, through the phone
            val trip = runBlocking { g.places.fs(alex.id).stat("SHARED/Trip") }
            g.transfers.copy(alex.id, trip, io.github.xattribution.burrow.data.Place.HOME, "DOCS", move = false)
            runBlocking { androidx.work.testing.TestListenableWorkerBuilder<io.github.xattribution.burrow.work.TransferWorker>(ctx).build().doWork() }
            assertEquals("sand and sun", File(server.lib, "DOCS/Trip/beach.txt").readText())
            assertTrue("a copy leaves the original", File(peer.lib, "SHARED/Trip/beach.txt").exists())

            // a drag-out address for a file there reads from there
            val beach = runBlocking { g.places.fs(alex.id).stat("SHARED/Trip/beach.txt") }
            val f = RemoteFileProvider.parse(RemoteFileProvider.uriFor(ctx, beach, alex.id))!!
            val buf = ByteArray(4)
            RemoteFileProvider.Reader(g.places.fs(f.place), f).run { assertEquals(4, onRead(0, 4, buf)); onRelease() }
            assertEquals("sand", String(buf))

            // Link to my NAS: this phone's Burrow (the phone is its admin) now reaches Alex's itself
            val msg = runBlocking { g.places.linkToHome(alex.id, homeIsAdmin = true) }
            assertTrue(msg, msg.contains("library"))
            assertTrue(server.web("GET", "/api/remotes").contains("Alex's Burrow"))
            assertTrue(server.web("GET", "/api/ls?path=" + java.net.URLEncoder.encode("Alex's Burrow/SHARED/Trip", "UTF-8")).contains("beach.txt"))

            smbPlace(ctx)

            // removing it unpairs this phone from Alex's Burrow
            runBlocking { g.places.remove(alex.id) }
            assertTrue(g.places.get(alex.id) == null)
            assertTrue(!peer.web("GET", "/api/devices").contains("Test phone"))
        }
    }

    /**
     * Unfolded: the phone's own storage beside Burrow. It asks for All files
     * access first; then files there can be picked and copied to the NAS.
     */
    @Test
    fun phoneStorageBesideBurrow() {
        pairAt(UNFOLDED)
        val app = ApplicationProvider.getApplicationContext<TestApp>()
        val top = android.os.Environment.getExternalStorageDirectory()
        File(top, "Download/report.pdf").apply { parentFile!!.mkdirs(); writeText("%PDF-1.4 report") }
        File(top, "Download/trip.txt").writeText("tickets")
        File(top, "Download/.pending").writeText("hidden")
        app.allFiles = false
        try {
            compose.onNodeWithContentDescription("More").performClick()
            compose.onNodeWithText("Show another place beside").performClick()
            waitFor("Burrow needs access to all files")
            shot("unfolded-phone-access")

            // the switch turned on in Android's settings, then back in the app
            app.allFiles = true
            compose.onAllNodesWithContentDescription("More")[1].performClick()
            compose.onNodeWithText("Refresh").performClick()
            waitFor("Download")
            compose.onNodeWithText("Download").performClick()
            waitFor("report.pdf")
            assertTrue("hidden files stay hidden", compose.onAllNodesWithText(".pending").fetchSemanticsNodes().isEmpty())
            assertTrue("Burrow stays on the left", compose.onAllNodesWithText("PHOTOS").fetchSemanticsNodes().isNotEmpty())
            shot("unfolded-beside")

            // hold to pick, tap to add, then copy both to the NAS
            compose.onNodeWithText("report.pdf").performTouchInput { longClick() }
            waitFor("1 selected")
            compose.onNodeWithText("trip.txt").performClick()
            waitFor("2 selected")
            shot("unfolded-phone-picked")
            compose.onNodeWithContentDescription("Copy to").performClick()
            waitFor("Copy 2 items")
            // DOCS in the picker, not the one in the list behind it
            compose.onNode(hasText("DOCS") and androidx.compose.ui.test.hasAnyAncestor(androidx.compose.ui.test.isDialog()) and androidx.compose.ui.test.hasClickAction()).performClick()
            waitFor("Into DOCS")
            compose.onNodeWithText("Copy here").performClick()
            runBlocking { androidx.work.testing.TestListenableWorkerBuilder<io.github.xattribution.burrow.work.TransferWorker>(app).build().doWork() }
            assertEquals("%PDF-1.4 report", File(server.lib, "DOCS/report.pdf").readText())
            assertEquals("tickets", File(server.lib, "DOCS/trip.txt").readText())
            assertTrue("a copy leaves the originals", File(top, "Download/report.pdf").exists())
        } finally {
            File(server.lib, "DOCS/report.pdf").delete()
            File(server.lib, "DOCS/trip.txt").delete()
            File(top, "Download").deleteRecursively()
            app.allFiles = false
        }
    }

    /** The same against a real Samba server, when ci/smb-server.sh is running (BURROW_TEST_SMB). */
    private fun smbPlace(ctx: android.content.Context) {
        val env = System.getenv("BURROW_TEST_SMB") ?: return
        val (host, port, share, user, pass, dir) = env.split(":", limit = 6).let { Six(it[0], it[1].toInt(), it[2], it[3], it[4], it[5]) }
        val sub = "app-${System.nanoTime()}"
        File(dir, "$sub/Scans").mkdirs()
        File(dir, "$sub/Scans/receipt.txt").writeText("paid in full")
        File(dir, sub).walk().forEach { it.setWritable(true, false); it.setReadable(true, false) }
        val g = ctx.graph
        val bad = runCatching { runBlocking { g.places.addSmb("Bad", io.github.xattribution.burrow.data.SmbSettings(host, port, share, user, "wrong", folder = sub)) } }
        assertTrue("wrong password: ${bad.exceptionOrNull()}", bad.exceptionOrNull()?.message.orEmpty().contains("turned down"))
        val office = runBlocking { g.places.addSmb("Office", io.github.xattribution.burrow.data.SmbSettings(host, port, share, user, pass, folder = sub)) }
        val fs = g.places.fs(office.id)
        runBlocking {
            assertEquals(listOf("Scans"), fs.ls("").entries.map { it.name })
            fs.open("Scans/receipt.txt", from = 5).use { assertEquals("in full", it.source.readUtf8()) }
            assertEquals("Scans/receipt (2).txt", fs.put("Scans", "receipt.txt", 3, 1_700_000_000_000, open = { okio.Buffer().writeUtf8("two") }))
            fs.mkdir("Old")
            assertEquals("Old/receipt.txt", fs.move("Scans/receipt.txt", "Old/receipt.txt"))
            assertTrue("a move doesn't replace", runCatching { fs.move("Scans/receipt (2).txt", "Old/receipt.txt") }.isFailure)
            fs.delete("Scans/receipt (2).txt")
        }
        assertEquals("paid in full", File(dir, "$sub/Old/receipt.txt").readText())
        assertTrue(!File(dir, "$sub/Scans/receipt (2).txt").exists())

        // move a file from this phone's Burrow onto the share
        val notes = runBlocking { g.places.fs("home").stat("DOCS/notes.txt") }
        g.transfers.copy("home", notes, office.id, "Scans", move = true)
        runBlocking { androidx.work.testing.TestListenableWorkerBuilder<io.github.xattribution.burrow.work.TransferWorker>(ctx).build().doWork() }
        assertEquals("hello notes", File(dir, "$sub/Scans/notes.txt").readText())
        assertTrue("the move took the original to Burrow's trash", !File(server.lib, "DOCS/notes.txt").exists())
        File(server.lib, "DOCS/notes.txt").writeText("hello notes") // other tests read it

        // browse it in the app: back to the top of the place first, where the switcher is
        compose.onAllNodesWithContentDescription("Alex's Burrow", useUnmergedTree = true)[0].performClick()
        waitFor("SHARED")
        switchPlace()
        compose.onNodeWithText("Office").performClick()
        waitFor("Old")
        shot("phone-place-smb")
        switchPlace()
        compose.onNodeWithText("Burrow").performClick()

        // link it to this phone's Burrow
        runBlocking { g.places.linkToHome(office.id, homeIsAdmin = true) }
        assertTrue(server.web("GET", "/api/ls?path=Office%2FOld").contains("receipt.txt"))
    }

    private fun switchPlace() {
        compose.waitForIdle()
        val n = compose.onAllNodesWithContentDescription("Switch place", useUnmergedTree = true).fetchSemanticsNodes()
        if (n.isEmpty()) {
            shot("fail-switch-place")
            throw AssertionError("no place switcher; on screen: " + collect(compose.onRoot().fetchSemanticsNode()) + " / unmerged: " + collectDesc(compose.onRoot(useUnmergedTree = true).fetchSemanticsNode()))
        }
        compose.onAllNodesWithContentDescription("Switch place", useUnmergedTree = true)[0].performClick()
    }

    private fun collectDesc(n: androidx.compose.ui.semantics.SemanticsNode): List<String> =
        n.config.getOrElseNullable(androidx.compose.ui.semantics.SemanticsProperties.ContentDescription) { null }.orEmpty() + n.children.flatMap { collectDesc(it) }

    private data class Six(val a: String, val b: Int, val c: String, val d: String, val e: String, val f: String)

    @Test
    fun foldableCoverScreen() {
        pairAt(COVER, "cover")
        compose.onNodeWithText("DOCS").performClick()
        waitFor("Manuals")
        shot("cover-folder")
        compose.onNodeWithText("Sync").performClick()
        waitFor("Nothing syncs yet")
        shot("cover-sync")
    }

    @Test
    fun foldableUnfolded() {
        pairAt(UNFOLDED, "unfolded")
        compose.onNodeWithText("DOCS").performClick()
        waitFor("recipes.md")
        compose.onNodeWithText("recipes.md").performClick()
        waitFor("Pancakes")
        // two panes side by side: the folder stays in view next to the file
        assertTrue(compose.onAllNodesWithText("notes.txt").fetchSemanticsNodes().isNotEmpty())
        shot("unfolded-two-pane")
        compose.onNodeWithText("Settings").performClick()
        waitFor("Recently deleted on Burrow")
        shot("unfolded-settings")
    }

    @Test
    fun tablet() {
        pairAt(TABLET, "tablet")
        compose.onNodeWithText("PHOTOS").performClick()
        waitFor("2024")
        shot("tablet-files")
        compose.onNodeWithText("Links").performClick()
        waitFor("No links yet")
        shot("tablet-links")
        compose.onNodeWithText("Sync").performClick()
        waitFor("Add a folder")
        compose.onAllNodesWithText("Add a folder")[0].performClick()
        waitFor("What on this phone?")
        shot("tablet-add-pair")
    }

    @Test
    fun aRemovedPhoneGoesBackToPairing() {
        pairAt(PHONE)
        val id = Regex("\"id\":\"([0-9a-f]+)\"").find(server.web("GET", "/api/devices"))!!.groupValues[1]
        server.web("DELETE", "/api/devices/$id")
        compose.onNodeWithText("DOCS").performClick()
        waitFor("Pair with Burrow")
        assertEquals(null, ApplicationProvider.getApplicationContext<BurrowApp>().graph.device.current)
    }

    @Suppress("unused")
    private fun unused() = listOf(hasText(""), compose.onNodeWithContentDescription(""))
}
