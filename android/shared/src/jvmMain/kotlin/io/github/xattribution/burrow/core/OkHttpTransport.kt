package io.github.xattribution.burrow.core

import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.internal.tls.OkHostnameVerifier
import okio.BufferedSink
import java.io.IOException
import java.net.ConnectException
import java.net.NoRouteToHostException
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import java.security.KeyStore
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLHandshakeException
import javax.net.ssl.SSLPeerUnverifiedException
import javax.net.ssl.SSLSession
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

private val octets = "application/octet-stream".toMediaType()

/** Posts channel messages with OkHttp. Bodies stream both ways. */
class OkHttpTransport(private val client: OkHttpClient) : Transport {
    override suspend fun post(url: String, body: OutgoingBody): IncomingResponse {
        val rb = object : okhttp3.RequestBody() {
            override fun contentType() = octets
            override fun contentLength() = body.length
            override fun writeTo(sink: BufferedSink) = body.write(sink)
        }
        val call = client.newCall(Request.Builder().url(url).post(rb).header("Cache-Control", "no-store").build())
        val resp = suspendCancellableCoroutine<okhttp3.Response> { cont ->
            cont.invokeOnCancellation { call.cancel() }
            call.enqueue(object : Callback {
                override fun onFailure(call: Call, e: IOException) = cont.resumeWithException(classify(e, url))
                override fun onResponse(call: Call, response: okhttp3.Response) = cont.resume(response)
            })
        }
        return IncomingResponse(resp.code, resp.body.source()) { resp.close() }
    }

    private fun classify(e: IOException, url: String): Exception {
        val connect = e is ConnectException || e is UnknownHostException || e is NoRouteToHostException ||
            e is SSLHandshakeException || e is SSLPeerUnverifiedException ||
            (e is SocketTimeoutException && (e.message.orEmpty().contains("connect", ignoreCase = true)))
        return if (connect) UnreachableException("Can't reach ${url.substringBefore("/api/")}: ${e.message}", e) else e
    }
}

/**
 * TLS for Burrow: publicly trusted certificates are checked as usual; Burrow's own
 * self-signed one is accepted only if its SHA-256 matches a pin learned at
 * pairing. The channel inside is end-to-end encrypted either way, so this is
 * defense in depth, not the only lock.
 */
object BurrowTls {
    fun fingerprint(cert: X509Certificate): String = b64url.encode(MessageDigest.getInstance("SHA-256").digest(cert.encoded))

    fun client(pins: () -> Set<String>, base: OkHttpClient = OkHttpClient()): OkHttpClient {
        val tmf = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(null as KeyStore?) }
        val system = tmf.trustManagers.filterIsInstance<X509TrustManager>().first()
        val tm = object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = system.checkClientTrusted(chain, authType)
            override fun getAcceptedIssuers(): Array<X509Certificate> = system.acceptedIssuers
            override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
                try {
                    system.checkServerTrusted(chain, authType)
                } catch (e: CertificateException) {
                    if (chain.isEmpty() || fingerprint(chain[0]) !in pins()) throw e
                }
            }
        }
        val ctx = SSLContext.getInstance("TLS").apply { init(null, arrayOf(tm), null) }
        fun pinned(s: SSLSession) = runCatching { fingerprint(s.peerCertificates[0] as X509Certificate) in pins() }.getOrDefault(false)
        return base.newBuilder()
            .sslSocketFactory(ctx.socketFactory, tm)
            .hostnameVerifier { host, session -> OkHostnameVerifier.verify(host, session) || pinned(session) }
            .connectTimeout(5, TimeUnit.SECONDS)
            .readTimeout(5, TimeUnit.MINUTES)
            .writeTimeout(5, TimeUnit.MINUTES)
            .build()
    }
}
