package io.github.xattribution.burrow

import android.content.Intent
import android.net.Uri
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.printToLog
import androidx.compose.ui.test.performClick
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
        val r = RemoteFileProvider.Reader(ctx.graph.connection, f)
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
