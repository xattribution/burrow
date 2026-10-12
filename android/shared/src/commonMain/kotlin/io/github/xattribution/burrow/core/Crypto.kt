package io.github.xattribution.burrow.core

/**
 * The primitives the channel needs, supplied per platform (BouncyCastle and the
 * JCA on Android and the JVM; CryptoKit would back an iPhone build).
 */
internal expect object Crypto {
    fun random(n: Int): ByteArray
    fun sha256(vararg parts: ByteArray): ByteArray
    fun hmacSha256(key: ByteArray, vararg parts: ByteArray): ByteArray

    /** A fresh ML-KEM-768 and X25519 key pair for one key exchange. */
    fun kemOffer(): KemOffer

    /** AES-256-GCM with a 16-byte tag. */
    fun aesGcm(key: ByteArray): Aead
}

internal interface KemOffer {
    /** ML-KEM-768 encapsulation key, 1184 bytes. */
    val mlkemPublic: ByteArray

    /** X25519 public key, 32 bytes. */
    val x25519Public: ByteArray

    /** Decapsulates the server's ciphertext and agrees on X25519: both shared secrets, concatenated. */
    fun finish(ciphertext: ByteArray, serverX25519: ByteArray): ByteArray
}

internal interface Aead {
    fun seal(nonce: ByteArray, plain: ByteArray, aad: ByteArray): ByteArray

    /** Throws [ChannelException] when authentication fails. */
    fun open(nonce: ByteArray, sealed: ByteArray, aad: ByteArray): ByteArray
}

// HKDF-SHA-256 (RFC 5869). Every key Burrow derives is 32 bytes, one block.

internal fun hkdfExtract(salt: ByteArray?, ikm: ByteArray): ByteArray =
    Crypto.hmacSha256(salt ?: ByteArray(32), ikm)

internal fun hkdfExpand(prk: ByteArray, info: ByteArray): ByteArray =
    Crypto.hmacSha256(prk, info, byteArrayOf(1))

internal fun hkdfExpand(prk: ByteArray, label: String): ByteArray = hkdfExpand(prk, label.encodeToByteArray())

/** HKDF with no salt, as Go's hkdf.Key(sha256.New, secret, nil, label, 32). */
internal fun hkdfKey(secret: ByteArray, label: String): ByteArray = hkdfExpand(hkdfExtract(null, secret), label)

internal fun constantTimeEquals(a: ByteArray, b: ByteArray): Boolean {
    if (a.size != b.size) return false
    var d = 0
    for (i in a.indices) d = d or (a[i].toInt() xor b[i].toInt())
    return d == 0
}

internal fun concat(vararg parts: ByteArray): ByteArray {
    val out = ByteArray(parts.sumOf { it.size })
    var off = 0
    for (p in parts) {
        p.copyInto(out, off)
        off += p.size
    }
    return out
}
