package io.github.xattribution.burrow.data

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.os.Build
import io.github.xattribution.burrow.BuildConfig
import io.github.xattribution.burrow.core.AccountDisabledException
import io.github.xattribution.burrow.core.BurrowApi
import io.github.xattribution.burrow.core.BurrowChannel
import io.github.xattribution.burrow.core.BurrowTls
import io.github.xattribution.burrow.core.DeviceInfo
import io.github.xattribution.burrow.core.DeviceKeys
import io.github.xattribution.burrow.core.NotPairedException
import io.github.xattribution.burrow.core.OkHttpTransport
import io.github.xattribution.burrow.core.PairResult
import io.github.xattribution.burrow.core.PairingLink
import io.github.xattribution.burrow.core.Routes
import io.github.xattribution.burrow.core.UnreachableException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.coroutines.cancellation.CancellationException

/** How the app is reaching Burrow right now. */
sealed interface Link {
    data object Unknown : Link
    /** [tethered]: through Burrow's tethersys bridge rather than one of its addresses. */
    data class Online(val atHome: Boolean, val base: String, val tethered: Boolean = false) : Link
    data class Offline(val message: String) : Link
    data class Disabled(val message: String) : Link
    data object Removed : Link
}

/**
 * Picks the address for the network the phone is on: the home address first on
 * Wi-Fi or Ethernet, the public one first elsewhere. Trying the home address
 * on a stranger's Wi-Fi is harmless: nothing reusable is ever sent, and a server
 * that isn't Burrow can't answer the handshake. The tethersys forward, when
 * Burrow offers one, comes last everywhere: it works from any network, but an
 * address of Burrow's own is the shorter way when one answers.
 */
class NetworkRoutes(
    context: Context,
    private val paired: () -> Paired?,
    private val tether: () -> String? = { null },
    private val openTether: suspend () -> Unit = {},
    onChange: () -> Unit,
) : Routes {
    private val cm = context.getSystemService(ConnectivityManager::class.java)

    init {
        runCatching {
            cm.registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) = onChange()
                override fun onLost(network: Network) = onChange()
            })
        }
    }

    fun onHomeNetwork(): Boolean {
        val caps = cm.getNetworkCapabilities(cm.activeNetwork) ?: return false
        return caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) || caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)
    }

    fun unmetered(): Boolean {
        val caps = cm.getNetworkCapabilities(cm.activeNetwork) ?: return false
        return caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
    }

    override suspend fun prepare() {
        if (paired()?.preference != RoutePreference.HOME) openTether()
    }

    override fun candidates(): List<String> {
        val p = paired() ?: return emptyList()
        val home = p.home
        val away = p.away
        val t = tether()
        return when (p.preference) {
            RoutePreference.HOME -> listOfNotNull(home)
            RoutePreference.AWAY -> listOfNotNull(away, t)
            RoutePreference.AUTO -> {
                val caps = cm.getNetworkCapabilities(cm.activeNetwork)
                val vpn = caps?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
                when {
                    onHomeNetwork() -> listOfNotNull(home, away, t)
                    vpn -> listOfNotNull(away, home, t)
                    else -> listOfNotNull(away ?: home, t)
                }
            }
        }.distinct()
    }
}

class Connection(context: Context, private val device: DeviceStore, private val tether: Tether? = null) {
    private val transport = OkHttpTransport(BurrowTls.client({ device.current?.info?.pins?.toSet().orEmpty() }))
    private val _status = MutableStateFlow<Link>(Link.Unknown)
    val status: StateFlow<Link> = _status
    private var cached: Pair<DeviceKeys, BurrowApi>? = null
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val enrolling = AtomicBoolean(false)
    @Volatile private var lastEnroll = 0L

    /** Burrow's tethersys bridge, while "Reach from anywhere" is on there. */
    private fun bridge(): String? = device.current?.info?.tether?.takeIf { it.isNotEmpty() }

    val routes = NetworkRoutes(
        context,
        paired = { device.current },
        tether = { bridge()?.let { tether?.openBase(it) } },
        openTether = { bridge()?.let { tether?.base(it) } },
    ) { synchronized(this) { cached?.second?.channel?.forgetRoute() } }

    /** The API for the paired server, or null when this phone isn't paired. */
    fun api(): BurrowApi? = synchronized(this) {
        val p = device.current ?: return null
        cached?.takeIf { it.first === p.keys }?.second
            ?: BurrowApi(BurrowChannel(transport, routes, p.keys, onServerInfo = { device.updateInfo(it) })).also { cached = p.keys to it }
    }

    /** Runs [block] against Burrow, keeping [status] current. */
    suspend fun <T> use(block: suspend (BurrowApi) -> T): T {
        val api = api() ?: throw NotPairedException("This phone isn't paired with Burrow.")
        try {
            val r = block(api)
            val base = api.channel.activeBase.orEmpty()
            _status.value = Link.Online(base.isNotEmpty() && base == device.current?.home, base, tether?.carries(base) == true)
            enroll(api)
            return r
        } catch (e: NotPairedException) {
            forget()
            throw e
        } catch (e: AccountDisabledException) {
            _status.value = Link.Disabled(e.message.orEmpty())
            throw e
        } catch (e: UnreachableException) {
            _status.value = Link.Offline(if (routes.candidates().isEmpty()) "No address to reach Burrow from this network." else "Can't reach Burrow right now.")
            throw e
        }
    }

    /**
     * Pairs with Burrow's tethersys bridge in the background, once Burrow says it
     * has one: an invite comes through the channel and is used right away. Tried
     * again at most every ten minutes if it fails.
     */
    private fun enroll(api: BurrowApi) {
        val t = tether ?: return
        val id = bridge() ?: return
        if (!t.usable || t.openBase(id) != null) return
        val now = System.currentTimeMillis()
        if (now - lastEnroll < 600_000 || !enrolling.compareAndSet(false, true)) return
        lastEnroll = now
        scope.launch {
            try {
                if (!t.knows(id)) {
                    val inv = api.tetherInvite()
                    if (inv.id == id) t.join(inv.invite)
                }
                if (t.base(id) != null) lastEnroll = 0
            } catch (e: Exception) {
                if (e is CancellationException) throw e
                // never the invite itself: it is a credential
                android.util.Log.w(Tether.TAG, "joining Burrow's bridge failed: ${e.javaClass.simpleName}: ${e.message}")
            } finally {
                enrolling.set(false)
            }
        }
    }

    suspend fun pair(link: PairingLink, name: String): PairResult {
        val tr = OkHttpTransport(BurrowTls.client({ setOfNotNull(link.pin) }))
        val res = BurrowChannel.pair(tr, link, DeviceInfo(name.ifBlank { Build.MODEL }, "android", "${Build.MANUFACTURER} ${Build.MODEL}".trim(), BuildConfig.VERSION_NAME))
        synchronized(this) { cached = null }
        device.save(res)
        _status.value = Link.Unknown
        return res
    }

    /** Removes this phone from Burrow (best effort) and forgets the keys. */
    suspend fun unpair() {
        runCatching { api()?.unpair() }
        forget()
    }

    private fun forget() {
        synchronized(this) { cached = null }
        bridge()?.let { id -> tether?.let { t -> scope.launch { t.forget(id) } } }
        device.clear()
        _status.value = Link.Removed
    }
}
