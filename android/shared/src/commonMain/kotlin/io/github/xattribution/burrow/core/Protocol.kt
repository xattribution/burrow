package io.github.xattribution.burrow.core

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okio.Buffer
import okio.BufferedSink
import okio.BufferedSource
import okio.Sink
import okio.Source
import okio.Timeout
import kotlin.io.encoding.Base64

/**
 * The client side of Burrow's phone channel. The server's devchan.go describes the
 * protocol; the byte layouts and key labels here must match it exactly.
 */
internal object Wire {
    const val VERSION: Byte = 1
    const val ID = 16
    const val NONCE = 32
    const val MAC = 32
    const val X = 32
    const val EK = 1184
    const val CT = 1088
    const val TAG = 16
    const val SEG_MAX = 64 * 1024
    const val HEADER_MAX = 64 * 1024
    const val LAST = 0x80000000L
    const val TUNNEL_HEAD = 1 + ID + 8
}

open class ChannelException(message: String) : Exception(message)

/** The server no longer knows this device (it was removed, or the account recovered). */
class NotPairedException(message: String) : ChannelException(message)

/** The account this device belongs to is turned off. */
class AccountDisabledException(message: String) : ChannelException(message)

internal val b64url = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT_OPTIONAL)

internal val json = Json { ignoreUnknownKeys = true; explicitNulls = false; encodeDefaults = false }

/** What the server tells a device about itself, inside the channel. */
@Serializable
data class ServerInfo(
    @SerialName("device_id") val deviceId: String = "",
    @SerialName("device_name") val deviceName: String = "",
    val user: String = "",
    val admin: Boolean = false,
    val lan: String = "",
    val public: String = "",
    val pins: List<String> = emptyList(),
    val version: String = "",
    val ttl: Int = 3600,
)

@Serializable
data class DeviceInfo(
    val name: String,
    val platform: String,
    val model: String = "",
    @SerialName("app_version") val appVersion: String = "",
)

/** A parsed burrow://pair link (the QR code's contents). */
class PairingLink(
    val codeId: ByteArray,
    val secret: ByteArray,
    val lan: String?,
    val public: String?,
    val pin: String?,
) {
    /** Addresses to try, home first. */
    val addresses: List<String> get() = listOfNotNull(lan, public)

    companion object {
        fun parse(link: String): PairingLink {
            val t = link.trim()
            require(t.startsWith("burrow://pair?")) { "That isn't a Burrow pairing code." }
            val q = parseQuery(t.substringAfter('?'))
            require(q["v"] == "1") { "This pairing code is from a newer Burrow. Update the app." }
            val code = runCatching { b64url.decode(q["c"].orEmpty()) }.getOrNull()
            val key = runCatching { b64url.decode(q["k"].orEmpty()) }.getOrNull()
            require(code?.size == Wire.ID && key?.size == 32) { "This pairing code is damaged. Show a new one." }
            val lan = q["lan"]?.takeIf { it.startsWith("https://") }
            val pub = q["pub"]?.takeIf { it.startsWith("https://") }
            require(lan != null || pub != null) { "This pairing code has no address to reach Burrow." }
            return PairingLink(code, key, lan, pub, q["pin"])
        }
    }
}

internal fun parseQuery(q: String): Map<String, String> =
    q.split('&').filter { it.isNotEmpty() }.associate {
        val k = it.substringBefore('=')
        val v = if ('=' in it) it.substringAfter('=') else ""
        percentDecode(k) to percentDecode(v)
    }

internal fun percentDecode(s: String): String {
    val out = Buffer()
    var i = 0
    while (i < s.length) {
        val c = s[i]
        when {
            c == '+' -> { out.writeByte(' '.code); i++ }
            c == '%' && i + 2 < s.length && s.substring(i + 1, i + 3).toIntOrNull(16) != null -> {
                out.writeByte(s.substring(i + 1, i + 3).toInt(16)); i += 3
            }
            else -> { out.writeUtf8CodePoint(c.code); i++ }
        }
    }
    return out.readUtf8()
}

internal fun percentEncode(s: String): String {
    val sb = StringBuilder()
    for (b in s.encodeToByteArray()) {
        val c = b.toInt() and 0xff
        if (c.toChar().isLetterOrDigit() && c < 128 || c.toChar() in "-._~") sb.append(c.toChar())
        else sb.append('%').append("0123456789ABCDEF"[c shr 4]).append("0123456789ABCDEF"[c and 15])
    }
    return sb.toString()
}

// ---- pairing and session handshakes ----

internal class Handshake(val request: ByteArray, val offer: KemOffer)

/** The keys a paired device keeps: its id and the 32-byte device key. */
class DeviceKeys(val deviceId: ByteArray, val deviceKey: ByteArray) {
    init {
        require(deviceId.size == Wire.ID && deviceKey.size == 32)
    }
}

class PairResult(val keys: DeviceKeys, val server: ServerInfo)

internal object Pairing {
    fun request(link: PairingLink, info: DeviceInfo): Handshake {
        val offer = Crypto.kemOffer()
        val nonce = Crypto.random(Wire.NONCE)
        val head = concat(byteArrayOf(Wire.VERSION), link.codeId, nonce, offer.mlkemPublic, offer.x25519Public)
        val plain = json.encodeToString(DeviceInfo.serializer(), info).encodeToByteArray()
        val n = plain.size + Wire.TAG
        val aad = concat(head, byteArrayOf((n shr 8).toByte(), n.toByte()))
        val sealed = Crypto.aesGcm(hkdfKey(concat(link.secret, nonce), "burrow-v1 pair info")).seal(ByteArray(12), plain, aad)
        val body = concat(aad, sealed)
        return Handshake(concat(body, Crypto.hmacSha256(hkdfKey(link.secret, "burrow-v1 pair auth"), body)), offer)
    }

    fun finish(link: PairingLink, hs: Handshake, reply: ByteArray): PairResult {
        val rh = 1 + Wire.ID + Wire.NONCE + Wire.CT + Wire.X
        if (reply.size <= rh || reply[0] != Wire.VERSION) throw ChannelException("Burrow sent a reply the app doesn't understand.")
        val ct = reply.copyOfRange(1 + Wire.ID + Wire.NONCE, 1 + Wire.ID + Wire.NONCE + Wire.CT)
        val sx = reply.copyOfRange(rh - Wire.X, rh)
        val ss = hs.offer.finish(ct, sx)
        val th = Crypto.sha256(hs.request, reply.copyOfRange(0, rh))
        val prk = hkdfExtract(th, concat(link.secret, ss))
        val payload = Crypto.aesGcm(hkdfExpand(prk, "burrow-v1 pair payload")).open(ByteArray(12), reply.copyOfRange(rh, reply.size), th)
        val keys = DeviceKeys(reply.copyOfRange(1, 1 + Wire.ID), hkdfExpand(prk, "burrow-v1 device key"))
        return PairResult(keys, json.decodeFromString(ServerInfo.serializer(), payload.decodeToString()))
    }
}

internal class Session(val id: ByteArray, val key: ByteArray, val expiresAt: Long, val server: ServerInfo)

internal object Hello {
    fun request(keys: DeviceKeys): Handshake {
        val offer = Crypto.kemOffer()
        val body = concat(byteArrayOf(Wire.VERSION), keys.deviceId, Crypto.random(Wire.NONCE), offer.mlkemPublic, offer.x25519Public)
        return Handshake(concat(body, Crypto.hmacSha256(hkdfKey(keys.deviceKey, "burrow-v1 hello auth"), body)), offer)
    }

    /**
     * Opens the server's reply. Only a server holding the device key can produce a
     * reply that opens, which is how the app knows it reached the real Burrow.
     */
    fun finish(keys: DeviceKeys, hs: Handshake, reply: ByteArray): Session {
        val rh = 1 + Wire.ID + Wire.NONCE + Wire.CT + Wire.X + 8
        if (reply.size <= rh || reply[0] != Wire.VERSION) throw ChannelException("Burrow sent a reply the app doesn't understand.")
        val ct = reply.copyOfRange(1 + Wire.ID + Wire.NONCE, 1 + Wire.ID + Wire.NONCE + Wire.CT)
        val sx = reply.copyOfRange(rh - 8 - Wire.X, rh - 8)
        val ss = hs.offer.finish(ct, sx)
        val th = Crypto.sha256(hs.request, reply.copyOfRange(0, rh))
        val prk = hkdfExtract(th, concat(keys.deviceKey, ss))
        val payload = try {
            Crypto.aesGcm(hkdfExpand(prk, "burrow-v1 hello payload")).open(ByteArray(12), reply.copyOfRange(rh, reply.size), th)
        } catch (e: ChannelException) {
            throw ChannelException("This server couldn't prove it is your Burrow.")
        }
        var exp = 0L
        for (i in rh - 8 until rh) exp = (exp shl 8) or (reply[i].toLong() and 0xff)
        return Session(
            reply.copyOfRange(1, 1 + Wire.ID), hkdfExpand(prk, "burrow-v1 session"), exp,
            json.decodeFromString(ServerInfo.serializer(), payload.decodeToString()),
        )
    }
}

// ---- tunnel messages ----

internal fun msgBytes(n: Long) = ByteArray(8) { (n shr (56 - 8 * it)).toByte() }

internal fun segNonce(i: Long, last: Boolean) = ByteArray(12).also {
    if (last) it[3] = 1
    for (k in 0 until 8) it[4 + k] = (i shr (56 - 8 * k)).toByte()
}

/** Size of a sealed tunnel request whose plaintext (header frame and body) is [plain] bytes. */
internal fun sealedLength(plain: Long): Long {
    val segs = maxOf(1L, (plain + Wire.SEG_MAX - 1) / Wire.SEG_MAX)
    return Wire.TUNNEL_HEAD + plain + segs * (4 + Wire.TAG)
}

/**
 * Encrypts a stream into segments, exactly as the server's segWriter: a full
 * segment goes out when more data follows; close() sends the last one.
 */
internal class SegmentWriter(private val out: BufferedSink, key: ByteArray, private val aad: ByteArray) : Sink {
    private val aead = Crypto.aesGcm(key)
    private val buf = ByteArray(Wire.SEG_MAX)
    private var len = 0
    private var n = 0L
    private var closed = false

    private fun emit(last: Boolean) {
        val sealed = aead.seal(segNonce(n, last), buf.copyOf(len), aad)
        var w = sealed.size.toLong()
        if (last) w = w or Wire.LAST
        out.writeInt(w.toInt())
        out.write(sealed)
        n++
        len = 0
    }

    fun write(b: ByteArray, off: Int = 0, count: Int = b.size) {
        check(!closed)
        var o = off
        var c = count
        while (c > 0) {
            if (len == Wire.SEG_MAX) emit(false)
            val k = minOf(c, Wire.SEG_MAX - len)
            b.copyInto(buf, len, o, o + k)
            len += k
            o += k
            c -= k
        }
    }

    override fun write(source: Buffer, byteCount: Long) {
        var left = byteCount
        val tmp = ByteArray(8192)
        while (left > 0) {
            val k = source.read(tmp, 0, minOf(left, tmp.size.toLong()).toInt())
            if (k < 0) break
            write(tmp, 0, k)
            left -= k
        }
    }

    override fun flush() = out.flush()
    override fun timeout(): Timeout = out.timeout()

    override fun close() {
        if (closed) return
        closed = true
        emit(true)
        out.flush()
    }
}

/** Decrypts segments; fails on tampering, reordering, truncation or trailing bytes. */
internal class SegmentReader(private val src: BufferedSource, key: ByteArray, private val aad: ByteArray) : Source {
    private val aead = Crypto.aesGcm(key)
    private val plain = Buffer()
    private var n = 0L
    private var done = false

    private fun next() {
        if (src.exhausted()) throw ChannelException("The reply ended early.")
        val w = src.readInt().toLong() and 0xffffffffL
        val last = w and Wire.LAST != 0L
        val l = (w and 0x7fffffffL).toInt()
        if (l < Wire.TAG || l > Wire.SEG_MAX + Wire.TAG) throw ChannelException("The reply is malformed.")
        val sealed = try { src.readByteArray(l.toLong()) } catch (e: Exception) { throw ChannelException("The reply ended early.") }
        plain.write(aead.open(segNonce(n, last), sealed, aad))
        n++
        if (last) {
            done = true
            if (!src.exhausted()) throw ChannelException("The reply is malformed.")
        }
    }

    override fun read(sink: Buffer, byteCount: Long): Long {
        while (plain.size == 0L) {
            if (done) return -1
            next()
        }
        return plain.read(sink, byteCount)
    }

    override fun timeout(): Timeout = src.timeout()
    override fun close() = src.close()
}
