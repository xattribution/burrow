package io.github.xattribution.burrow.core

import kotlinx.coroutines.runBlocking
import okio.Buffer
import okio.buffer
import okio.source
import org.junit.AfterClass
import org.junit.BeforeClass
import java.io.File
import java.security.MessageDigest
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/** The Kotlin client against the real Go server: the two must agree byte for byte. */
class ChannelInteropTest {
    companion object {
        lateinit var server: TestServer

        @BeforeClass @JvmStatic
        fun up() {
            server = TestServer()
        }

        @AfterClass @JvmStatic
        fun down() = server.close()
    }

    private fun transport(pins: Set<String>) = OkHttpTransport(BurrowTls.client({ pins }))

    private fun pair(name: String = "Test Phone"): Pair<PairResult, PairingLink> = runBlocking {
        val link = PairingLink.parse(server.pairingLink())
        val res = BurrowChannel.pair(transport(setOfNotNull(link.pin)), link, DeviceInfo(name, "android", "Fold 7", "1.0"))
        res to link
    }

    private fun api(keys: DeviceKeys, pins: Set<String>, routes: Routes = StaticRoutes(listOf(server.lan))) =
        BurrowApi(BurrowChannel(transport(pins), routes, keys))

    @Test
    fun pairBrowseAndRead() = runBlocking {
        val (res, link) = pair()
        assertEquals("admin", res.server.user)
        assertEquals(server.lan, res.server.lan)
        assertEquals(server.public, res.server.public)
        assertEquals(link.pin, res.server.pins.first())
        val api = api(res.keys, res.server.pins.toSet())
        val ls = api.ls("DOCS")
        assertTrue(ls.entries.any { it.name == "notes.txt" }, ls.toString())
        assertTrue(ls.canWrite)
        api.raw("DOCS/notes.txt", from = 6).use { assertEquals("notes", it.body.readUtf8()) }
        assertEquals("Fold 7", server.web("GET", "/api/devices").let { Regex("\"model\":\"([^\"]*)\"").find(it)!!.groupValues[1] })
        // the code works once
        val again = assertFailsWith<ChannelException> {
            BurrowChannel.pair(transport(setOfNotNull(link.pin)), link, DeviceInfo("x", "android"))
        }
        assertTrue(again.message!!.contains("already used"), again.message)
    }

    @Test
    fun uploadInChunksAndSync() = runBlocking {
        val (res, _) = pair()
        val api = api(res.keys, res.server.pins.toSet())
        val data = ByteArray(300_000) { (it * 31).toByte() }
        val sha = MessageDigest.getInstance("SHA-256").digest(data).joinToString("") { "%02x".format(it) }
        val up = api.startUpload("PHOTOS", "clip.bin", data.size.toLong(), 1_700_000_000_123, sha)
        var off = 0L
        while (off < data.size) {
            val end = minOf(off + 128_000, data.size.toLong())
            val from = off
            off = api.putChunk(up.id, off, end - off) { it.write(data, from.toInt(), (end - from).toInt()) }
        }
        val done = api.finishUpload(up.id)
        assertEquals("PHOTOS/clip.bin", done.path)
        assertEquals(sha, done.sha256)
        assertEquals(1_700_000_000_123, done.mtime)
        api.raw("PHOTOS/clip.bin").use { assertContentEquals(data, it.body.readByteArray()) }
        val l = api.syncList("PHOTOS")
        val e = l.entries.single { it.path == "clip.bin" }
        assertEquals(sha, e.hash)
        assertTrue(l.writable)
        // move, delete to trash, restore
        api.mkdir("PHOTOS/2024")
        assertEquals("PHOTOS/2024/clip.bin", api.move("PHOTOS/clip.bin", "PHOTOS/2024/clip.bin"))
        val del = api.deleteIfUnchanged("PHOTOS/2024/clip.bin", data.size.toLong(), 1_700_000_000_123)
        assertTrue(del.ok, del.toString())
        val t = api.trash().single { it.path == "PHOTOS/2024/clip.bin" }
        assertEquals("PHOTOS/2024/clip.bin", api.restore(t.id))
    }

    @Test
    fun handshakeRacesAddressesOnANewNetwork() = runBlocking {
        val (res, _) = pair()
        // the home address on a stranger's network: something that takes the
        // connection and never answers, and a web server that isn't Burrow
        val silent = java.net.ServerSocket(0)
        val held = mutableListOf<java.net.Socket>()
        Thread { runCatching { while (true) held += silent.accept() } }.apply { isDaemon = true }.start()
        val notBurrow = com.sun.net.httpserver.HttpServer.create(java.net.InetSocketAddress("127.0.0.1", 0), 0).apply {
            createContext("/") { it.sendResponseHeaders(404, -1); it.close() }
            start()
        }
        try {
            val routes = StaticRoutes(listOf("https://127.0.0.1:${silent.localPort}", "http://127.0.0.1:${notBurrow.address.port}", server.lan))
            val api = api(res.keys, res.server.pins.toSet(), routes)
            val t0 = System.nanoTime()
            assertTrue(api.ls("DOCS").entries.isNotEmpty())
            val ms = (System.nanoTime() - t0) / 1_000_000
            // two staggers, not a connect or read timeout
            assertTrue(ms < 3_000, "took $ms ms")
            assertEquals(server.lan, api.channel.activeBase)
        } finally {
            notBurrow.stop(0)
            held.forEach { it.close() }
            silent.close()
        }
    }

    @Test
    fun sessionSurvivesServerRestart() = runBlocking {
        val (res, _) = pair()
        val api = api(res.keys, res.server.pins.toSet())
        api.ls("DOCS")
        server.restart() // every session is gone; the app handshakes again on its own
        assertTrue(api.ls("DOCS").entries.isNotEmpty())
    }

    @Test
    fun fallsBackToTheOtherAddress() = runBlocking {
        val (res, _) = pair()
        val dead = "https://127.0.0.1:1"
        val seen = mutableListOf<String>()
        val routes = object : Routes {
            override fun candidates() = listOf(dead, server.public)
            override fun succeeded(base: String) { seen += "ok $base" }
            override fun failed(base: String) { seen += "fail $base" }
        }
        val api = api(res.keys, res.server.pins.toSet(), routes)
        assertTrue(api.ls("DOCS").entries.isNotEmpty())
        assertEquals(listOf("fail $dead", "ok ${server.public}"), seen.take(2))
    }

    @Test
    fun unpinnedSelfSignedCertificateIsRefused() = runBlocking {
        val (res, _) = pair()
        val api = api(res.keys, emptySet())
        assertFailsWith<UnreachableException> { api.ls("DOCS") }
        Unit
    }

    @Test
    fun aFakeServerCantFinishTheHandshake() = runBlocking {
        val (res, _) = pair()
        // something that answers like Burrow but doesn't hold the device key
        val fake = object : Transport {
            override suspend fun post(url: String, body: OutgoingBody): IncomingResponse {
                val reply = Buffer().write(byteArrayOf(1)).write(Crypto.random(Wire.ID + Wire.NONCE))
                    .write(fakeCiphertext()).write(Crypto.kemOffer().x25519Public).writeLong(nowSeconds() + 3600).write(Crypto.random(200))
                return IncomingResponse(200, reply) {}
            }
        }
        val ch = BurrowChannel(fake, StaticRoutes(listOf("https://evil.example")), res.keys)
        val e = assertFailsWith<ChannelException> { ch.call("GET", "/api/ls") }
        assertTrue(e.message!!.contains("couldn't prove"), e.message)
    }

    private fun fakeCiphertext(): ByteArray = Crypto.random(Wire.CT)

    @Test
    fun aRevokedPhoneIsTold() = runBlocking {
        val (res, _) = pair("Doomed")
        val api = api(res.keys, res.server.pins.toSet())
        api.ls("DOCS")
        val id = Regex("\"id\":\"([0-9a-f]+)\",\"user_id\":\\d+,\"user\":\"[^\"]*\",\"name\":\"Doomed\"").find(server.web("GET", "/api/devices"))!!.groupValues[1]
        server.web("DELETE", "/api/devices/$id")
        assertFailsWith<NotPairedException> { api.ls("DOCS") }
        Unit
    }

    @Test
    fun segmentsMatchTheServer() {
        // lengths around segment boundaries round-trip, and sealedLength predicts the size
        for (n in listOf(0, 1, Wire.SEG_MAX - 1, Wire.SEG_MAX, Wire.SEG_MAX + 1, 3 * Wire.SEG_MAX + 7)) {
            val key = Crypto.random(32)
            val head = Crypto.random(Wire.TUNNEL_HEAD)
            val out = Buffer()
            val w = SegmentWriter(out, key, head).buffer()
            val data = Crypto.random(n)
            w.write(data)
            w.close()
            assertEquals(sealedLength(n.toLong()) - Wire.TUNNEL_HEAD, out.size, "size for $n")
            val back = SegmentReader(out, key, head).buffer().readByteArray()
            assertContentEquals(data, back)
        }
        val key = Crypto.random(32)
        val out = Buffer()
        SegmentWriter(out, key, byteArrayOf(1)).buffer().apply { write(Crypto.random(Wire.SEG_MAX * 2)); close() }
        val bytes = out.readByteArray()
        bytes[10] = (bytes[10].toInt() xor 1).toByte()
        assertFailsWith<ChannelException> { SegmentReader(Buffer().write(bytes), key, byteArrayOf(1)).buffer().readByteArray() }
        assertFailsWith<ChannelException> { SegmentReader(Buffer().write(bytes.copyOf(bytes.size - 30)), key, byteArrayOf(1)).buffer().readByteArray() }
    }

    @Test
    fun pairingLinksParse() {
        val l = PairingLink.parse("burrow://pair?c=AAAAAAAAAAAAAAAAAAAAAA&k=" + "A".repeat(43) + "&lan=https%3A%2F%2F192.168.1.2%3A47823&pin=abc&pub=https%3A%2F%2Ffiles.example.com&v=1")
        assertEquals("https://192.168.1.2:47823", l.lan)
        assertEquals("https://files.example.com", l.public)
        assertFailsWith<IllegalArgumentException> { PairingLink.parse("https://example.com") }
        assertFailsWith<IllegalArgumentException> { PairingLink.parse("burrow://pair?v=1&c=AA&k=BB&lan=https%3A%2F%2Fx") }
        assertFailsWith<IllegalArgumentException> { PairingLink.parse("burrow://pair?v=1&c=AAAAAAAAAAAAAAAAAAAAAA&k=" + "A".repeat(43) + "&lan=http%3A%2F%2Fplain") }
    }

    @Suppress("unused")
    private fun File.sha() = MessageDigest.getInstance("SHA-256").digest(source().buffer().readByteArray())
}
