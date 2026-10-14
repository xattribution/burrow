package io.github.xattribution.burrow

import android.content.ContentValues
import android.os.Environment
import android.provider.MediaStore
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.github.xattribution.burrow.core.PairingLink
import io.github.xattribution.burrow.core.SyncEngine
import io.github.xattribution.burrow.data.DeviceStore
import io.github.xattribution.burrow.data.KeystoreWrapper
import io.github.xattribution.burrow.data.MediaTree
import io.github.xattribution.burrow.data.localTreeFor
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * On a real Android (an emulator in CI), against a real Burrow: the device key
 * in the Android Keystore, a photo backup from the media library, and the same
 * through WorkManager in the background. The pairing link comes in as the
 * instrumentation argument "link".
 */
@RunWith(AndroidJUnit4::class)
class DeviceTest {
    private val app = ApplicationProvider.getApplicationContext<BurrowApp>()

    private fun insertPhoto(name: String, bytes: ByteArray) {
        val cr = app.contentResolver
        val v = ContentValues().apply {
            put(MediaStore.Images.Media.DISPLAY_NAME, name)
            put(MediaStore.Images.Media.MIME_TYPE, "image/jpeg")
            put(MediaStore.Images.Media.RELATIVE_PATH, Environment.DIRECTORY_PICTURES + "/BurrowTest")
            put(MediaStore.Images.Media.IS_PENDING, 1)
        }
        val uri = cr.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, v)!!
        cr.openOutputStream(uri)!!.use { it.write(bytes) }
        cr.update(uri, ContentValues().apply { put(MediaStore.Images.Media.IS_PENDING, 0) }, null, null)
    }

    @Test
    fun pairsAndBacksUpPhotos() = runBlocking {
        val link = InstrumentationRegistry.getArguments().getString("link") ?: error("run with -e link burrow://pair?...")
        val g = app.graph
        if (g.device.current != null) g.connection.unpair()
        g.connection.pair(PairingLink.parse(link), "CI emulator")
        val paired = g.device.current
        assertNotNull(paired)
        // the key is sealed in the Keystore and opens again from storage
        assertArrayEquals(paired!!.keys.deviceKey, DeviceStore(app, KeystoreWrapper()).current!!.keys.deviceKey)

        val roots = g.connection.use { it.ls("") }.entries.map { it.name }
        assertTrue("libraries: $roots", "DOCS" in roots && "PHOTOS" in roots)
        g.connection.use { api -> api.raw("DOCS/hello.txt").use { assertEquals("hello from the NAS\n", it.body.readUtf8()) } }

        // a photo backup straight through the engine
        val stamp = System.currentTimeMillis()
        val photo = ByteArray(300_000) { (it * 7).toByte() }
        insertPhoto("ci-$stamp.jpg", photo)
        g.db.pairQueries.insert("Photos", MediaTree.ALL, "Photos and videos", "PHOTOS/ci-$stamp", "backup", 0, 0, stamp / 1000)
        val pair = g.db.pairQueries.all().executeAsList().last()
        val rep = g.connection.use { SyncEngine(it, g.db).sync(pair, localTreeFor(app, pair.local_uri)) }
        assertTrue("report: $rep", rep.uploaded >= 1 && rep.errors.isEmpty())
        val remote = "PHOTOS/ci-$stamp/Pictures/BurrowTest/ci-$stamp.jpg"
        g.connection.use { api -> api.raw(remote).use { assertArrayEquals(photo, it.body.readByteArray()) } }

        // the same file dragged out to another app: read through the content address, seeking as a player would
        val uri = io.github.xattribution.burrow.data.RemoteFileProvider.uriFor(app, io.github.xattribution.burrow.core.Entry(path = remote, name = "ci-$stamp.jpg", size = photo.size.toLong()))
        assertEquals("image/jpeg", app.contentResolver.getType(uri))
        app.contentResolver.openFileDescriptor(uri, "r")!!.use { pfd ->
            assertEquals(photo.size.toLong(), pfd.statSize)
            java.io.FileInputStream(pfd.fileDescriptor).channel.use { ch ->
                val tail = java.nio.ByteBuffer.allocate(1000)
                ch.position(200_000L)
                while (tail.hasRemaining() && ch.read(tail) > 0) Unit
                assertArrayEquals(photo.copyOfRange(200_000, 201_000), tail.array())
                val head = java.nio.ByteBuffer.allocate(photo.size)
                ch.position(0)
                while (head.hasRemaining() && ch.read(head) > 0) Unit
                assertArrayEquals(photo, head.array())
            }
        }

        // and in the background: a new photo, a sync through WorkManager
        insertPhoto("ci-$stamp-2.jpg", photo.reversedArray())
        g.scheduler.syncNow(pair.id)
        val second = "Pictures/BurrowTest/ci-$stamp-2.jpg"
        var seen = false
        for (i in 0 until 60) {
            seen = g.connection.use { it.syncList("PHOTOS/ci-$stamp") }.entries.any { it.path == second }
            if (seen) break
            delay(1000)
        }
        assertTrue("the background sync didn't upload the second photo", seen)
    }
}
