package io.github.xattribution.burrow.data

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import io.github.xattribution.burrow.core.DeviceKeys
import io.github.xattribution.burrow.core.PairResult
import io.github.xattribution.burrow.core.ServerInfo
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.serialization.json.Json
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** Encrypts the device key at rest. */
interface KeyWrapper {
    fun wrap(plain: ByteArray): ByteArray
    fun unwrap(sealed: ByteArray): ByteArray
    fun forget() {}
}

/**
 * Wraps the device key with an AES key that lives in the Android Keystore
 * (hardware-backed where the phone has it) and can't be exported, so the key
 * file alone is useless off this phone.
 */
class KeystoreWrapper(private val alias: String = "burrow-device-key") : KeyWrapper {
    private val ks get() = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }

    private fun key(): SecretKey {
        (ks.getEntry(alias, null) as? KeyStore.SecretKeyEntry)?.let { return it.secretKey }
        val g = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        g.init(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return g.generateKey()
    }

    override fun wrap(plain: ByteArray): ByteArray {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, key())
        return c.iv + c.doFinal(plain)
    }

    override fun unwrap(sealed: ByteArray): ByteArray {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, sealed, 0, 12))
        return c.doFinal(sealed, 12, sealed.size - 12)
    }

    override fun forget() {
        runCatching { ks.deleteEntry(alias) }
    }
}

/** Which address to use. */
enum class RoutePreference { AUTO, HOME, AWAY }

/** This phone's pairing: its keys and what it knows about the server. */
data class Paired(val keys: DeviceKeys, val info: ServerInfo, val preference: RoutePreference) {
    val home get() = info.lan.takeIf { it.isNotBlank() }
    val away get() = info.public.takeIf { it.isNotBlank() }
}

/** One pairing, kept in its own preferences file: "device" for this phone's Burrow, "peer-<id>" for others. */
class DeviceStore(context: Context, private val wrapper: KeyWrapper, file: String = "device") {
    private val prefs = context.getSharedPreferences(file, Context.MODE_PRIVATE)
    private val json = Json { ignoreUnknownKeys = true }
    private val _state = MutableStateFlow(load())
    val state: StateFlow<Paired?> = _state
    val current: Paired? get() = _state.value

    private fun load(): Paired? {
        val id = prefs.getString("id", null) ?: return null
        val sealed = prefs.getString("key", null) ?: return null
        val info = prefs.getString("info", null)?.let { runCatching { json.decodeFromString(ServerInfo.serializer(), it) }.getOrNull() } ?: return null
        val key = runCatching { wrapper.unwrap(Base64.decode(sealed, Base64.NO_WRAP)) }.getOrNull() ?: return null
        val pref = runCatching { RoutePreference.valueOf(prefs.getString("route", "AUTO")!!) }.getOrDefault(RoutePreference.AUTO)
        return Paired(DeviceKeys(Base64.decode(id, Base64.NO_WRAP), key), info, pref)
    }

    fun save(result: PairResult) {
        prefs.edit()
            .putString("id", Base64.encodeToString(result.keys.deviceId, Base64.NO_WRAP))
            .putString("key", Base64.encodeToString(wrapper.wrap(result.keys.deviceKey), Base64.NO_WRAP))
            .putString("info", json.encodeToString(ServerInfo.serializer(), result.server))
            .putString("route", RoutePreference.AUTO.name)
            .commit()
        _state.value = load()
    }

    /** The server's addresses, certificate pins and account details, as last reported. */
    fun updateInfo(info: ServerInfo) {
        val cur = current ?: return
        // an empty address in a reply means "not set"; keep pins learned before if none came
        val merged = info.copy(pins = info.pins.ifEmpty { cur.info.pins })
        if (merged == cur.info) return
        prefs.edit().putString("info", json.encodeToString(ServerInfo.serializer(), merged)).apply()
        _state.value = cur.copy(info = merged)
    }

    fun setPreference(p: RoutePreference) {
        val cur = current ?: return
        prefs.edit().putString("route", p.name).apply()
        _state.value = cur.copy(preference = p)
    }

    fun clear() {
        prefs.edit().clear().commit()
        wrapper.forget()
        _state.value = null
    }
}
