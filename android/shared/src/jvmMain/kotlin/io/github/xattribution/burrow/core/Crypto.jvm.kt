package io.github.xattribution.burrow.core

import org.bouncycastle.crypto.agreement.X25519Agreement
import org.bouncycastle.crypto.generators.X25519KeyPairGenerator
import org.bouncycastle.crypto.params.X25519KeyGenerationParameters
import org.bouncycastle.crypto.params.X25519PrivateKeyParameters
import org.bouncycastle.crypto.params.X25519PublicKeyParameters
import org.bouncycastle.crypto.generators.MLKEMKeyPairGenerator
import org.bouncycastle.crypto.kems.MLKEMExtractor
import org.bouncycastle.crypto.params.MLKEMKeyGenerationParameters
import org.bouncycastle.crypto.params.MLKEMParameters
import org.bouncycastle.crypto.params.MLKEMPrivateKeyParameters
import org.bouncycastle.crypto.params.MLKEMPublicKeyParameters
import java.security.MessageDigest
import java.security.SecureRandom
import javax.crypto.AEADBadTagException
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

// ML-KEM-768 and X25519 come from BouncyCastle's lightweight API (the same code on
// every Android version); SHA-256, HMAC and AES-GCM from the platform, which is
// hardware-accelerated on phones.
internal actual object Crypto {
    private val rng = SecureRandom()

    actual fun random(n: Int): ByteArray = ByteArray(n).also { rng.nextBytes(it) }

    actual fun sha256(vararg parts: ByteArray): ByteArray {
        val d = MessageDigest.getInstance("SHA-256")
        for (p in parts) d.update(p)
        return d.digest()
    }

    actual fun hmacSha256(key: ByteArray, vararg parts: ByteArray): ByteArray {
        val m = Mac.getInstance("HmacSHA256")
        m.init(SecretKeySpec(key, "HmacSHA256"))
        for (p in parts) m.update(p)
        return m.doFinal()
    }

    actual fun kemOffer(): KemOffer {
        val kg = MLKEMKeyPairGenerator().apply { init(MLKEMKeyGenerationParameters(rng, MLKEMParameters.ml_kem_768)) }
        val kp = kg.generateKeyPair()
        val xg = X25519KeyPairGenerator().apply { init(X25519KeyGenerationParameters(rng)) }
        val xp = xg.generateKeyPair()
        return object : KemOffer {
            override val mlkemPublic: ByteArray = (kp.public as MLKEMPublicKeyParameters).encoded
            override val x25519Public: ByteArray = (xp.public as X25519PublicKeyParameters).encoded

            override fun finish(ciphertext: ByteArray, serverX25519: ByteArray): ByteArray {
                val ss1 = try {
                    MLKEMExtractor(kp.private as MLKEMPrivateKeyParameters).extractSecret(ciphertext)
                } catch (e: Exception) {
                    throw ChannelException("Burrow's key exchange reply is malformed.")
                }
                val ss2 = ByteArray(32)
                try {
                    X25519Agreement().apply { init(xp.private as X25519PrivateKeyParameters) }
                        .calculateAgreement(X25519PublicKeyParameters(serverX25519, 0), ss2, 0)
                } catch (e: Exception) {
                    throw ChannelException("Burrow's key exchange reply is malformed.")
                }
                return concat(ss1, ss2)
            }
        }
    }

    actual fun aesGcm(key: ByteArray): Aead {
        require(key.size == 32)
        val k = SecretKeySpec(key, "AES")
        return object : Aead {
            private val enc = Cipher.getInstance("AES/GCM/NoPadding")
            private val dec = Cipher.getInstance("AES/GCM/NoPadding")

            override fun seal(nonce: ByteArray, plain: ByteArray, aad: ByteArray): ByteArray {
                enc.init(Cipher.ENCRYPT_MODE, k, GCMParameterSpec(128, nonce))
                enc.updateAAD(aad)
                return enc.doFinal(plain)
            }

            override fun open(nonce: ByteArray, sealed: ByteArray, aad: ByteArray): ByteArray {
                try {
                    dec.init(Cipher.DECRYPT_MODE, k, GCMParameterSpec(128, nonce))
                    dec.updateAAD(aad)
                    return dec.doFinal(sealed)
                } catch (e: AEADBadTagException) {
                    throw ChannelException("A message failed authentication.")
                } catch (e: java.security.GeneralSecurityException) {
                    throw ChannelException("A message failed authentication.")
                }
            }
        }
    }
}
