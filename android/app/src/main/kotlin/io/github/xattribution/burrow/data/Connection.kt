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
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/** How the app is reaching Burrow right now. */
sealed interface Link {
    data object Unknown : Link
    data class Online(val atHome: Boolean, val base: String) : Link
    data class Offline(val message: String) : Link
    data class Disabled(val message: String) : Link
    data object Removed : Link
}

/**
 * Picks the address for the network the phone is on: the home address first on
 * Wi-Fi or Ethernet, the public one first elsewhere. Trying the home address
 * on a stranger's Wi-Fi is harmless: nothing reusable is ever sent, and a server
 * that isn't Burrow can't answer the handshake.
 */
class NetworkRoutes(context: Context, private val paired: () -> Paired?, onChange: () -> Unit) : Routes {
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

    override fun candidates(): List<String> {
        val p = paired() ?: return emptyList()
        val home = p.home
        val away = p.away
        return when (p.preference) {
            RoutePreference.HOME -> listOfNotNull(home)
            RoutePreference.AWAY -> listOfNotNull(away)
            RoutePreference.AUTO -> {
                val caps = cm.getNetworkCapabilities(cm.activeNetwork)
                val vpn = caps?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
                when {
                    onHomeNetwork() -> listOfNotNull(home, away)
                    vpn -> listOfNotNull(away, home)
                    else -> listOfNotNull(away ?: home)
                }
            }
        }
    }
}

class Connection(context: Context, private val device: DeviceStore) {
    private val transport = OkHttpTransport(BurrowTls.client({ device.current?.info?.pins?.toSet().orEmpty() }))
    private val _status = MutableStateFlow<Link>(Link.Unknown)
    val status: StateFlow<Link> = _status
    private var cached: Pair<DeviceKeys, BurrowApi>? = null
    val routes = NetworkRoutes(context, { device.current }) { synchronized(this) { cached?.second?.channel?.forgetRoute() } }

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
            _status.value = Link.Online(base.isNotEmpty() && base == device.current?.home, base)
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
        device.clear()
        _status.value = Link.Removed
    }
}
