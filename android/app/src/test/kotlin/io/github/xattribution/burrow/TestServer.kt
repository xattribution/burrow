package io.github.xattribution.burrow

import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.File
import java.net.ServerSocket
import java.nio.file.Files
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

/**
 * A real Burrow server for interop tests, built from the Go code in the repo, on
 * loopback with a temporary library. [web] is a signed-in admin browser session.
 */
class TestServer(private val roots: List<String> = listOf("DOCS", "PHOTOS"), private val env: Map<String, String> = emptyMap()) : AutoCloseable {
    val dir: File = Files.createTempDirectory("burrow-test").toFile()
    val lib = File(dir, "lib")
    private val state = File(dir, "state")
    val lanPort = freePort()
    val publicPort = freePort()
    val lan = "https://127.0.0.1:$lanPort"
    val public = "https://127.0.0.1:$publicPort"
    private var proc: Process? = null

    fun start() {
        val pb = ProcessBuilder(binary.absolutePath)
        pb.environment().apply {
            put("BURROW_DATA", lib.absolutePath)
            put("BURROW_STATE", state.absolutePath)
            put("BURROW_DROP", File(dir, "drop").absolutePath)
            put("BURROW_ADDR", "127.0.0.1:$lanPort")
            put("BURROW_PUBLIC_ADDR", "127.0.0.1:$publicPort")
            put("BURROW_WATCH", "true")
            putAll(env)
        }
        pb.redirectErrorStream(true).redirectOutput(File(dir, "server.log"))
        proc = pb.start()
        val deadline = System.currentTimeMillis() + 20_000
        while (System.currentTimeMillis() < deadline) {
            val ok = runCatching { insecure.newCall(Request.Builder().url("$lan/healthz").build()).execute().use { it.code == 200 } }.getOrDefault(false)
            if (ok) return
            Thread.sleep(100)
        }
        error("server didn't start: " + File(dir, "server.log").readText())
    }

    fun stop() {
        proc?.destroy()
        proc?.waitFor(10, TimeUnit.SECONDS)
        proc = null
    }

    fun restart() {
        stop()
        start()
    }

    private val cookies = mutableListOf<Cookie>()
    private val browser = insecure.newBuilder().cookieJar(object : CookieJar {
        override fun saveFromResponse(url: HttpUrl, cookies: List<Cookie>) {
            this@TestServer.cookies.removeAll { c -> cookies.any { it.name == c.name } }
            this@TestServer.cookies.addAll(cookies)
        }
        override fun loadForRequest(url: HttpUrl) = cookies.toList()
    }).build()

    /** A web-UI API call as the signed-in admin. */
    fun web(method: String, path: String, body: String? = null): String {
        val rb = body?.toRequestBody("application/json".toMediaType())
        val req = Request.Builder().url(lan + path).header("X-Burrow", "1").method(method, rb ?: if (method == "GET" || method == "DELETE") null else "".toRequestBody()).build()
        browser.newCall(req).execute().use { r ->
            val t = r.body.string()
            check(r.isSuccessful) { "$method $path: ${r.code} $t" }
            return t
        }
    }

    /** A fresh pairing link from the web UI. */
    fun pairingLink(): String = Regex("\"link\":\"([^\"]+)\"").find(web("POST", "/api/devices/pair"))!!.groupValues[1].replace("\\u0026", "&")

    init {
        for (r in roots) File(lib, r).mkdirs()
        File(lib, "DOCS/notes.txt").writeText("hello notes")
        File(lib, "DOCS/Taxes").mkdirs()
        File(lib, "DOCS/Taxes/2024.pdf").writeText("pdf")
        start()
        web("POST", "/api/setup", """{"user":"admin","password":"longenough1","roots":${roots.joinToString(",", "[", "]") { "\"$it\"" }}}""")
        web("PUT", "/api/settings", """{"lan_url":"$lan","public_url":"$public"}""")
    }

    override fun close() {
        stop()
        dir.deleteRecursively()
    }

    companion object {
        private fun freePort() = ServerSocket(0).use { it.localPort }

        /** Loopback only: the harness itself doesn't check certificates. */
        val insecure: OkHttpClient by lazy {
            val tm = object : X509TrustManager {
                override fun checkClientTrusted(c: Array<X509Certificate>, a: String) {}
                override fun checkServerTrusted(c: Array<X509Certificate>, a: String) {}
                override fun getAcceptedIssuers() = arrayOf<X509Certificate>()
            }
            val ctx = SSLContext.getInstance("TLS").apply { init(null, arrayOf(tm), SecureRandom()) }
            OkHttpClient.Builder().sslSocketFactory(ctx.socketFactory, tm).hostnameVerifier { _, _ -> true }
                .proxy(java.net.Proxy.NO_PROXY).build()
        }

        /** The Go server, built once per test run. */
        val binary: File by lazy {
            val repo = File(System.getProperty("burrow.repo") ?: "../..")
            val out = File(repo, "android/build/burrow-test-server")
            out.parentFile.mkdirs()
            val p = ProcessBuilder("go", "build", "-o", out.absolutePath, ".").directory(repo).redirectErrorStream(true).start()
            val log = p.inputStream.bufferedReader().readText()
            check(p.waitFor() == 0) { "go build failed: $log" }
            out
        }
    }
}
