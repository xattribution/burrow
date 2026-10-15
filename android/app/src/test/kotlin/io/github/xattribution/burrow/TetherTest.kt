package io.github.xattribution.burrow

import androidx.test.core.app.ApplicationProvider
import io.github.xattribution.burrow.core.NotPairedException
import io.github.xattribution.burrow.core.PairingLink
import io.github.xattribution.burrow.data.Connection
import io.github.xattribution.burrow.data.DeviceStore
import io.github.xattribution.burrow.data.Link
import io.github.xattribution.burrow.data.RoutePreference
import io.github.xattribution.burrow.data.Tether
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import uniffi.tethersys_ffi.FfiProfile
import java.io.File
import java.nio.file.Files

/**
 * Reach from anywhere, end to end: the real server runs the real bridge, and the
 * app's own Connection pairs with it through the channel, then reaches Burrow
 * through the tethersys forward when its own addresses don't answer. Needs the
 * bridge (tether/target/release/burrow-tether) and a host build of the library
 * (android/tether/build.sh), as CI builds; skipped without them.
 */
@RunWith(RobolectricTestRunner::class)
class TetherTest {
    private val repo = File(System.getProperty("burrow.repo") ?: "../..")
    private val bridge = File(repo, "tether/target/release/burrow-tether")
    private val lib = File(System.getProperty("jna.library.path").orEmpty(), "libtethersys_ffi.so")

    private suspend fun <T> eventually(what: String, ms: Long = 30_000, f: suspend () -> T?): T =
        try {
            withTimeout(ms) {
                var v = f()
                while (v == null) {
                    delay(100)
                    v = f()
                }
                v
            }
        } catch (e: Exception) {
            throw AssertionError("waiting for $what", e)
        }

    @Test
    fun reachesBurrowOverTethersys() = runBlocking {
        assumeTrue("needs the bridge: cargo build --release in tether/", bridge.canExecute())
        assumeTrue("needs the library: android/tether/build.sh", lib.exists())
        TestServer(env = mapOf("BURROW_TETHER_BIN" to bridge.absolutePath, "BURROW_TETHER_LOCAL" to "1")).use { server ->
            server.web("PUT", "/api/settings", """{"tether":true}""")
            eventually("the bridge") { server.web("GET", "/api/tether").takeIf { "\"running\":true" in it } }

            val ctx = ApplicationProvider.getApplicationContext<TestApp>()
            val store = DeviceStore(ctx, ctx.keyWrapper("tether-test"), "tether-test")
            val tether = Tether(Files.createTempDirectory("tether-phone").toFile(), { "Test phone" }, FfiProfile.LOCAL)
            val conn = Connection(ctx, store, tether)
            conn.pair(PairingLink.parse(server.pairingLink()), "Test phone")
            conn.use { it.ls("") }
            val id = store.current!!.info.tether
            assertTrue("server info names the bridge", id.isNotEmpty())

            // the phone pairs with the bridge by itself, through the channel
            eventually("the phone to join the bridge") { tether.openBase(id) }
            assertTrue(tether.knows(id))
            assertTrue("the server lists the phone", "\"devices\":1" in server.web("GET", "/api/tether"))

            // Burrow's own addresses stop answering: the forward carries on
            server.web("PUT", "/api/settings", """{"public_url":"https://127.0.0.1:9"}""")
            store.setPreference(RoutePreference.AWAY)
            conn.api()!!.channel.reset()
            conn.use { it.ls("") } // a new handshake learns the dead address
            conn.api()!!.channel.reset()
            conn.api()!!.channel.forgetRoute()
            val listing = conn.use { it.ls("DOCS") }
            assertTrue(listing.entries.any { it.name == "notes.txt" })
            val st = conn.status.value as Link.Online
            assertTrue("went through tethersys: $st", st.tethered)
            assertTrue("\"last_via\":\"tether\"" in server.web("GET", "/api/devices"))

            // removed on the server: the bridge drops the phone, and the phone forgets the bridge
            val dev = Regex("\"id\":\"([0-9a-f]+)\"").find(server.web("GET", "/api/devices"))!!.groupValues[1]
            server.web("DELETE", "/api/devices/$dev")
            conn.api()!!.channel.reset()
            val err = runCatching { conn.use { it.ls("") } }.exceptionOrNull()
            assertTrue("removed: $err", err is NotPairedException)
            eventually("the phone to forget the bridge") { if (tether.knows(id)) null else true }
            assertEquals(null, store.current)
        }
    }
}
