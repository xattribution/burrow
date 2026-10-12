@file:OptIn(ExperimentalAtomicApi::class, ExperimentalTime::class)

package io.github.xattribution.burrow.core

import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import okio.Buffer
import okio.BufferedSink
import okio.BufferedSource
import okio.buffer
import kotlin.concurrent.atomics.AtomicLong
import kotlin.concurrent.atomics.ExperimentalAtomicApi
import kotlin.concurrent.atomics.incrementAndFetch
import kotlin.time.Clock
import kotlin.time.ExperimentalTime

/** Moves bytes to and from Burrow over HTTPS; the platform supplies it (OkHttp). */
interface Transport {
    /** POSTs [body] to [url] and returns the response while its body is still streaming. */
    suspend fun post(url: String, body: OutgoingBody): IncomingResponse
}

/** A request body of [length] bytes (-1 when unknown), written on demand. */
class OutgoingBody(val length: Long, val write: (BufferedSink) -> Unit)

class IncomingResponse(val code: Int, val body: BufferedSource, private val release: () -> Unit) : AutoCloseable {
    override fun close() = release()
}

/** The connection couldn't be made at all, so the request never left; another address may work. */
class UnreachableException(message: String, cause: Throwable? = null) : Exception(message, cause)

/** Which base addresses to try, best first, and what happened when they were tried. */
interface Routes {
    fun candidates(): List<String>
    fun succeeded(base: String) {}
    fun failed(base: String) {}
}

class StaticRoutes(private val bases: List<String>) : Routes {
    override fun candidates() = bases
}

/** A request body inside the channel. [write] may be called again if the request is retried. */
class RequestBody(val length: Long, val contentType: String? = null, val write: (BufferedSink) -> Unit) {
    companion object {
        fun json(text: String): RequestBody {
            val b = text.encodeToByteArray()
            return RequestBody(b.size.toLong(), "application/json") { it.write(b) }
        }

        fun bytes(b: ByteArray, contentType: String = "application/octet-stream") = RequestBody(b.size.toLong(), contentType) { it.write(b) }
    }
}

/** A reply from inside the channel. Close it (or read it with [use]) when done. */
class Response(val status: Int, val headers: Map<String, String>, val body: BufferedSource, private val release: () -> Unit) : AutoCloseable {
    fun header(name: String): String? = headers.entries.firstOrNull { it.key.equals(name, ignoreCase = true) }?.value
    val ok: Boolean get() = status in 200..299
    fun text(): String = use { body.readUtf8() }
    override fun close() = release()
}

@Serializable
private data class InnerRequest(val m: String, val u: String, val h: Map<String, String> = emptyMap(), val l: Long = 0)

@Serializable
private data class InnerReply(val s: Int, val h: Map<String, String> = emptyMap())

@Serializable
internal data class ErrorBody(val error: String = "")

internal fun nowSeconds(): Long = Clock.System.now().epochSeconds

/**
 * One paired device's connection to Burrow. Sessions are made on demand and
 * renewed before they run out; every request is sealed end to end.
 */
class BurrowChannel(
    private val transport: Transport,
    private val routes: Routes,
    private val keys: DeviceKeys,
    private val now: () -> Long = ::nowSeconds,
    private val onServerInfo: (ServerInfo) -> Unit = {},
) {
    private class Live(val session: Session, val counter: AtomicLong = AtomicLong(0))

    private val lock = Mutex()
    private var live: Live? = null
    private var base: String? = null

    /** The address the last request went to. */
    val activeBase: String? get() = base

    /** What the server last said about itself (addresses, pins, account). */
    val server: ServerInfo? get() = live?.session?.server

    private suspend fun current(renew: Boolean): Live = lock.withLock {
        val l = live
        if (!renew && l != null && l.session.expiresAt - 120 > now()) return l
        val hs = Hello.request(keys)
        val resp = postAnywhere("/api/d/hello", OutgoingBody(hs.request.size.toLong()) { it.write(hs.request) })
        val reply = resp.use { r ->
            val bytes = r.body.readByteArray()
            when (r.code) {
                200 -> bytes
                401 -> throw NotPairedException("This phone was removed from Burrow. Pair it again.")
                403 -> throw AccountDisabledException(errorText(bytes, "Your Burrow account is turned off."))
                429 -> throw ChannelException("Too many attempts. Try again in 15 minutes.")
                else -> throw ChannelException(errorText(bytes, "Burrow answered ${r.code}."))
            }
        }
        val s = Hello.finish(keys, hs, reply)
        onServerInfo(s.server)
        Live(s).also { live = it }
    }

    /** Forgets the session (after the app was in the background for long, say). */
    suspend fun reset() = lock.withLock { live = null }

    private suspend fun postAnywhere(path: String, body: OutgoingBody): IncomingResponse {
        val tried = mutableListOf<String>()
        val order = (listOfNotNull(base) + routes.candidates()).distinct()
        var last: Exception? = null
        for (b in order) {
            tried += b
            try {
                val r = transport.post(b.trimEnd('/') + path, body)
                base = b
                routes.succeeded(b)
                return r
            } catch (e: UnreachableException) {
                routes.failed(b)
                if (base == b) base = null
                last = e
            }
        }
        throw UnreachableException("Can't reach Burrow (tried ${tried.joinToString()}).", last)
    }

    /**
     * Sends one request through the channel: [method] and [pathAndQuery] as the web
     * API takes them ("/api/ls?path=DOCS"). The reply's status is whatever the API said.
     */
    suspend fun call(method: String, pathAndQuery: String, headers: Map<String, String> = emptyMap(), body: RequestBody? = null): Response =
        withContext(ioDispatcher) { callIO(method, pathAndQuery, headers, body) }

    private suspend fun callIO(method: String, pathAndQuery: String, headers: Map<String, String>, body: RequestBody?): Response {
        var renew = false
        while (true) {
            val l = current(renew)
            val s = l.session
            val n = l.counter.incrementAndFetch()
            val head = concat(byteArrayOf(Wire.VERSION), s.id, msgBytes(n))
            val h = if (body?.contentType != null) headers + ("Content-Type" to body.contentType) else headers
            val frame = json.encodeToString(InnerRequest.serializer(), InnerRequest(method, pathAndQuery, h, body?.length ?: 0)).encodeToByteArray()
            val plain = 4L + frame.size + (body?.length ?: 0)
            val c2s = hkdfExpand(s.key, concat("burrow-v1 c2s ".encodeToByteArray(), msgBytes(n)))
            val out = OutgoingBody(if (body != null && body.length < 0) -1 else sealedLength(plain)) { sink ->
                sink.write(head)
                val w = SegmentWriter(sink, c2s, head).buffer()
                w.writeInt(frame.size)
                w.write(frame)
                body?.write?.invoke(w)
                w.close()
            }
            val resp = postAnywhere("/api/d", out)
            when (resp.code) {
                200 -> {
                    if (resp.body.readByte() != Wire.VERSION) {
                        resp.close()
                        throw ChannelException("Burrow sent a reply the app doesn't understand.")
                    }
                    val s2c = hkdfExpand(s.key, concat("burrow-v1 s2c ".encodeToByteArray(), msgBytes(n)))
                    val reader = SegmentReader(resp.body, s2c, head).buffer()
                    val ir = try {
                        val len = reader.readInt()
                        if (len < 0 || len > Wire.HEADER_MAX) throw ChannelException("The reply is malformed.")
                        json.decodeFromString(InnerReply.serializer(), reader.readUtf8(len.toLong()))
                    } catch (e: Exception) {
                        resp.close()
                        throw if (e is ChannelException) e else ChannelException("The reply is malformed.")
                    }
                    return Response(ir.s, ir.h, reader) { resp.close() }
                }
                401 -> {
                    val msg = resp.use { errorText(it.body.readByteArray(), "") }
                    if (msg == "session" && !renew) {
                        renew = true // the server restarted or the session ran out: new handshake, same request
                        continue
                    }
                    throw NotPairedException("This phone was removed from Burrow. Pair it again.")
                }
                403 -> throw AccountDisabledException(resp.use { errorText(it.body.readByteArray(), "Your Burrow account is turned off.") })
                else -> throw ChannelException(resp.use { errorText(it.body.readByteArray(), "Burrow answered ${it.code}.") })
            }
        }
    }

    companion object {
        /** Completes pairing from a scanned link. */
        suspend fun pair(transport: Transport, link: PairingLink, info: DeviceInfo): PairResult = withContext(ioDispatcher) {
            val hs = Pairing.request(link, info)
            var last: Exception? = null
            for (b in link.addresses) {
                val resp = try {
                    transport.post(b.trimEnd('/') + "/api/d/pair", OutgoingBody(hs.request.size.toLong()) { it.write(hs.request) })
                } catch (e: UnreachableException) {
                    last = e
                    continue
                }
                val bytes = resp.use { it.body.readByteArray() }
                return@withContext when (resp.code) {
                    200 -> Pairing.finish(link, hs, bytes)
                    403 -> throw ChannelException(errorText(bytes, "This pairing code is wrong, expired or already used. Show a new one."))
                    429 -> throw ChannelException("Too many attempts. Try again in 15 minutes.")
                    else -> throw ChannelException(errorText(bytes, "Burrow answered ${resp.code}."))
                }
            }
            throw UnreachableException("Can't reach Burrow at ${link.addresses.joinToString(" or ")}.", last)
        }
    }
}

internal fun errorText(body: ByteArray, fallback: String): String =
    runCatching { json.decodeFromString(ErrorBody.serializer(), body.decodeToString()).error }.getOrNull()?.takeIf { it.isNotBlank() } ?: fallback

/** Reads a whole small reply into a Buffer (for tests and tiny bodies). */
internal fun BufferedSource.drain(): Buffer = Buffer().also { readAll(it) }
