package io.github.xattribution.burrow.data

import android.util.Log
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import uniffi.tethersys_ffi.EventListener
import uniffi.tethersys_ffi.FfiConfig
import uniffi.tethersys_ffi.FfiEvent
import uniffi.tethersys_ffi.FfiLink
import uniffi.tethersys_ffi.FfiProfile
import uniffi.tethersys_ffi.TethersysNode
import java.io.File
import java.util.concurrent.ConcurrentHashMap
import kotlin.coroutines.cancellation.CancellationException

/**
 * Reaching Burrow over tethersys, for a Burrow with "Reach from anywhere" on.
 * One tethersys node for the app pairs with each Burrow's bridge and forwards a
 * loopback port to it; that port is one more address for the channel, which runs
 * inside unchanged (end-to-end encrypted, certificate pinned). tethersys only
 * finds the way: straight across the LAN or the internet when it can, through a
 * relay that sees ciphertext when it can't. Without the native library (a phone
 * with an ABI the APK doesn't carry) it simply isn't used.
 */
class Tether(private val dir: File, private val name: () -> String, private val profile: FfiProfile = FfiProfile.PUBLIC) {
    private val lock = Mutex()
    @Volatile private var node: TethersysNode? = null
    @Volatile private var broken = false
    private val ports = ConcurrentHashMap<String, Int>()
    private val _links = MutableStateFlow<Map<String, FfiLink>>(emptyMap())

    /** How each bridge is reached right now (direct or relayed), by bridge id. */
    val links: StateFlow<Map<String, FfiLink>> = _links

    /** False once the native library failed to load. */
    val usable: Boolean get() = !broken

    private val listener = object : EventListener {
        override fun onEvent(event: FfiEvent) {
            when (event) {
                is FfiEvent.Connected -> _links.update { it + (event.peer to event.link) }
                is FfiEvent.LinkChanged -> _links.update { it + (event.peer to event.link) }
                else -> Unit
            }
        }
    }

    private suspend fun node(): TethersysNode? {
        node?.let { return it }
        if (broken) return null
        return lock.withLock {
            node ?: try {
                dir.mkdirs()
                TethersysNode.start(FfiConfig(name(), dir.path, profile, null, null, false, null), listener).also { node = it }
            } catch (e: Throwable) {
                if (e is CancellationException) throw e
                // UnsatisfiedLinkError and friends: no library for this phone
                Log.w(TAG, "tethersys unavailable: $e")
                broken = true
                null
            }
        }
    }

    /** Whether this phone is paired with bridge [id]. */
    suspend fun knows(id: String): Boolean = node()?.peers()?.any { it.id == id } == true

    /** Pairs with a bridge using a one-use [invite] from its Burrow; returns the bridge id. */
    suspend fun join(invite: String): String? = node()?.join(invite)?.id

    /** The local address that leads to bridge [id], or null when this phone isn't paired with it. */
    suspend fun base(id: String): String? {
        ports[id]?.let { return url(it) }
        val n = node() ?: return null
        if (n.peers().none { it.id == id }) return null
        val port = try {
            n.forwardTcp(id).toInt()
        } catch (e: Exception) {
            if (e is CancellationException) throw e
            return null
        }
        ports[id] = port
        return url(port)
    }

    /** [base] if it is already open; never starts anything. */
    fun openBase(id: String): String? = ports[id]?.let(::url)

    /** Whether [base] is one of this app's forwards. */
    fun carries(base: String?): Boolean = base != null && ports.values.any { url(it) == base }

    /** Unpairs from bridge [id] (this phone was removed from that Burrow). */
    suspend fun forget(id: String) {
        val n = node() ?: return
        ports.remove(id)
        runCatching { n.stopForward(id) }
        runCatching { n.removePeer(id) }
        _links.update { it - id }
    }

    private fun url(port: Int) = "https://127.0.0.1:$port"

    companion object {
        const val TAG = "BurrowTether"
    }
}
