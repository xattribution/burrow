package io.github.xattribution.burrow.data

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import androidx.core.content.ContextCompat
import android.util.Base64
import io.github.xattribution.burrow.core.PairingLink
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.security.SecureRandom

/** A place the Files tab can show. */
sealed interface Place {
    val id: String
    val name: String

    /** The Burrow this phone is paired with: backups, sync and links live here. */
    data class Home(override val name: String) : Place {
        override val id get() = HOME
    }

    /** Another Burrow this phone paired with on its own, someone else's say. */
    data class Peer(override val id: String, override val name: String, val store: DeviceStore, val conn: Connection) : Place

    /** An SMB share reached from the phone. */
    data class Smb(override val id: String, override val name: String, val settings: SmbSettings) : Place

    /** This phone's own storage, a memory card or a USB drive. */
    data class Phone(override val id: String, override val name: String, val root: java.io.File, val removable: Boolean) : Place

    /** A folder another app shares through the system's document picker (a cloud drive app, say). */
    data class Folder(override val id: String, override val name: String, val tree: android.net.Uri) : Place

    companion object {
        const val HOME = "home"
        const val PHONE = "phone"
    }
}

@Serializable
private data class PlaceRecord(val id: String, val kind: String, val name: String, val sealed: String = "")

/**
 * The places the Files tab shows: this phone's Burrow, the phone's own storage
 * volumes, and connections added in the app. Connections stay on this phone:
 * their keys and passwords are sealed with the Android Keystore and never sent
 * to this phone's own Burrow, unless "Link to my NAS" hands one over on purpose.
 */
class Places(
    private val app: Context,
    private val home: Connection,
    private val tether: Tether?,
    private val wrapper: (String) -> KeyWrapper,
    private val phoneAccess: () -> Boolean = { PhoneAccess.granted(app) },
) {
    private val prefs = app.getSharedPreferences("places", Context.MODE_PRIVATE)
    private val json = Json { ignoreUnknownKeys = true }
    private val sealer by lazy { wrapper("burrow-places") }
    // one Connection per paired Burrow for the life of the app, kept across reloads
    private val peers = mutableMapOf<String, Place.Peer>()
    private var volumes = phoneVolumes(app)
    private val _list = MutableStateFlow(load())
    val list: StateFlow<List<Place>> = _list
    private val fs = mutableMapOf<String, PlaceFs>()

    private fun records(): List<PlaceRecord> =
        prefs.getString("list", null)?.let { runCatching { json.decodeFromString(ListSerializer(PlaceRecord.serializer()), it) }.getOrNull() }.orEmpty()

    private fun saveRecords(rs: List<PlaceRecord>) {
        prefs.edit().putString("list", json.encodeToString(ListSerializer(PlaceRecord.serializer()), rs)).commit()
        _list.value = load()
    }

    init {
        // memory cards and USB drives come and go
        val f = IntentFilter().apply {
            listOf(Intent.ACTION_MEDIA_MOUNTED, Intent.ACTION_MEDIA_UNMOUNTED, Intent.ACTION_MEDIA_REMOVED, Intent.ACTION_MEDIA_EJECT, Intent.ACTION_MEDIA_BAD_REMOVAL).forEach(::addAction)
            addDataScheme("file")
        }
        runCatching {
            ContextCompat.registerReceiver(app, object : BroadcastReceiver() {
                override fun onReceive(c: Context, i: Intent) = refreshVolumes()
            }, f, ContextCompat.RECEIVER_NOT_EXPORTED)
        }
    }

    /** Reads the mounted volumes again. */
    fun refreshVolumes() {
        val now = phoneVolumes(app)
        if (now == volumes) return
        volumes = now
        synchronized(fs) { fs.keys.filter { it == Place.PHONE || it.startsWith("vol-") }.forEach { fs.remove(it) } }
        _list.value = load()
    }

    private fun load(): List<Place> = listOf<Place>(Place.Home("Burrow")) + volumes.map { Place.Phone(it.id, it.name, it.root, it.removable) } + records().mapNotNull { r ->
        when (r.kind) {
            "burrow" -> peers[r.id]?.copy(name = r.name) ?: run {
                val store = DeviceStore(app, wrapper("burrow-peer-${r.id}"), "peer-${r.id}")
                if (store.current == null) null else Place.Peer(r.id, r.name, store, Connection(app, store, tether)).also { peers[r.id] = it }
            }
            "smb" -> runCatching {
                val plain = sealer.unwrap(Base64.decode(r.sealed, Base64.NO_WRAP))
                Place.Smb(r.id, r.name, json.decodeFromString(SmbSettings.serializer(), plain.decodeToString()))
            }.getOrNull()
            "folder" -> Place.Folder(r.id, r.name, android.net.Uri.parse(r.sealed))
            else -> null
        }
    }

    fun get(id: String): Place? = _list.value.firstOrNull { it.id == id }

    /** The way to browse [id]; this phone's Burrow when it's gone. */
    fun fs(id: String): PlaceFs = synchronized(fs) {
        fs.getOrPut(id) {
            when (val p = get(id)) {
                is Place.Peer -> BurrowFs(p.id, p.conn)
                is Place.Smb -> SmbFs(p.id, p.settings)
                is Place.Phone -> PhoneFs(p.id, p.root, phoneAccess)
                is Place.Folder -> DocFs(p.id, app, p.tree)
                else -> BurrowFs(Place.HOME, home)
            }
        }
    }

    private fun newId(): String {
        val b = ByteArray(6)
        SecureRandom().nextBytes(b)
        return b.joinToString("") { "%02x".format(it) }
    }

    private fun unique(name: String): String {
        val taken = _list.value.map { it.name.lowercase() }.toSet()
        if (name.lowercase() !in taken) return name
        return (2..99).map { "$name $it" }.first { it.lowercase() !in taken }
    }

    /** Pairs with another Burrow from its pairing link, on its own keys. */
    suspend fun pairPeer(link: PairingLink, name: String, deviceName: String): Place.Peer {
        val id = newId()
        val store = DeviceStore(app, wrapper("burrow-peer-$id"), "peer-$id")
        val conn = Connection(app, store, tether)
        try {
            conn.pair(link, deviceName)
        } catch (e: Exception) {
            store.clear()
            app.deleteSharedPreferences("peer-$id")
            throw e
        }
        val shown = unique(name.ifBlank { "Other Burrow" })
        peers[id] = Place.Peer(id, shown, store, conn)
        saveRecords(records() + PlaceRecord(id, "burrow", shown))
        return get(id) as Place.Peer
    }

    /** Adds an SMB share after signing in to it once, so a typo shows up now. */
    suspend fun addSmb(name: String, s: SmbSettings): Place.Smb {
        SmbFs.check(s)
        val id = newId()
        val sealed = Base64.encodeToString(sealer.wrap(json.encodeToString(SmbSettings.serializer(), s).encodeToByteArray()), Base64.NO_WRAP)
        saveRecords(records() + PlaceRecord(id, "smb", unique(name.ifBlank { s.share }), sealed))
        return get(id) as Place.Smb
    }

    /** Adds a folder picked with the system's document picker; the grant is already persisted. */
    fun addFolder(tree: android.net.Uri): Place.Folder {
        records().firstOrNull { it.kind == "folder" && it.sealed == tree.toString() }?.let { return get(it.id) as Place.Folder }
        val id = newId()
        saveRecords(records() + PlaceRecord(id, "folder", unique(DocFs.nameOf(app, tree)), tree.toString()))
        return get(id) as Place.Folder
    }

    /**
     * "Link to my NAS": hands a connection to this phone's own Burrow, which then
     * reaches it itself as a library (Settings, Connected NAS there). An SMB share
     * goes over with its sign-in; for another Burrow, a REST-only token is made
     * on it for this account, so the link reaches what this account can. The
     * connection stays on the phone too. Admins only, as on the web.
     */
    suspend fun linkToHome(id: String, homeIsAdmin: Boolean): String {
        if (!homeIsAdmin) throw IllegalStateException("Only an admin of your Burrow can link a NAS to it.")
        val tries: List<JsonObject> = when (val p = get(id)) {
            is Place.Smb -> buildJsonObject {
                put("name", p.name); put("kind", "smb"); put("host", p.settings.host); put("port", p.settings.port)
                put("share", p.settings.share); put("user", p.settings.user); put("password", p.settings.password)
                put("domain", p.settings.domain); put("folder", p.settings.folder)
            }.let { listOf(it) }
            is Place.Peer -> {
                val info = p.store.current?.info ?: throw IllegalStateException("This phone isn't paired with ${p.name} any more.")
                val urls = listOf(info.public, info.lan).filter { it.isNotBlank() }.distinct()
                if (urls.isEmpty()) throw IllegalStateException("${p.name} has no address your Burrow can reach.")
                val tok = p.conn.use { it.linkToken() }
                // the public address first (it works from anywhere), the home one if the public port doesn't serve
                // the API; and without the pin first, since a reverse proxy has a public certificate it wouldn't match
                urls.flatMap { url ->
                    listOf("", tok.pin).distinct().map { pin ->
                        buildJsonObject { put("name", p.name); put("kind", "burrow"); put("url", url); put("token", tok.token); if (pin.isNotEmpty()) put("pin", pin) }
                    }
                }
            }
            is Place.Phone, is Place.Folder -> throw IllegalStateException("Only another Burrow or an SMB share can be linked to your NAS.")
            else -> throw IllegalStateException("That's already your Burrow.")
        }
        var last: Exception? = null
        for (c in tries) {
            try {
                home.use { it.addRemote(c) }
                return "${get(id)?.name} is now a library on your Burrow too."
            } catch (e: io.github.xattribution.burrow.core.ApiException) {
                last = e
                if (e.status != 400) throw e
            }
        }
        throw last ?: IllegalStateException("Couldn't link it.")
    }

    /** Forgets a connection; another Burrow is told to remove this phone. */
    suspend fun remove(id: String) {
        val p = get(id) ?: return
        synchronized(fs) { fs.remove(id)?.close() }
        if (p is Place.Phone) return
        if (p is Place.Peer) {
            runCatching { p.conn.unpair() }
            p.store.clear()
            peers.remove(id)
            app.deleteSharedPreferences("peer-$id")
        }
        if (p is Place.Folder) {
            runCatching { app.contentResolver.releasePersistableUriPermission(p.tree, android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION or android.content.Intent.FLAG_GRANT_WRITE_URI_PERMISSION) }
        }
        saveRecords(records().filter { it.id != id })
    }
}
