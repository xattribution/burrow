package io.github.xattribution.burrow.core

import kotlinx.coroutines.withContext
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import kotlinx.serialization.json.add
import okio.BufferedSink

@Serializable
data class Entry(
    val path: String,
    val name: String,
    val dir: Boolean = false,
    val size: Long = 0,
    val mtime: Long = 0,
    val kind: String = "",
    val ext: String = "",
    val shared: Int = 0,
    val fav: Boolean = false,
)

@Serializable
data class Listing(
    val path: String,
    val entries: List<Entry>,
    @SerialName("can_share") val canShare: Boolean = false,
    @SerialName("can_write") val canWrite: Boolean = false,
)

@Serializable
data class SearchResult(val entries: List<Entry>, val total: Int = 0, @SerialName("took_ms") val tookMs: Double = 0.0)

@Serializable
data class Share(
    val id: String,
    val path: String,
    val dir: Boolean = false,
    val mode: String = "link",
    val label: String = "",
    val note: String = "",
    val created: Long = 0,
    val expires: Long = 0,
    @SerialName("has_password") val hasPassword: Boolean = false,
    @SerialName("max_downloads") val maxDownloads: Long = 0,
    val downloads: Long = 0,
    val revoked: Boolean = false,
    @SerialName("last_access") val lastAccess: Long = 0,
    @SerialName("allow_upload") val allowUpload: Boolean = false,
    val items: Int = 0,
    val state: String = "active",
    val url: String = "",
    val owner: String = "",
)

@Serializable
data class Info(
    val entry: Entry,
    val shares: List<Share> = emptyList(),
    @SerialName("can_read") val canRead: Boolean = false,
    @SerialName("can_share") val canShare: Boolean = false,
    @SerialName("can_change") val canChange: Boolean = false,
    val preview: String = "",
    val items: Long = 0,
    @SerialName("total_size") val totalSize: Long = 0,
)

@Serializable
data class TrashItem(
    val id: String,
    val path: String,
    val name: String,
    val dir: Boolean = false,
    val size: Long = 0,
    val deleted: Long = 0,
    val user: String = "",
)

@Serializable
data class DeleteResult(val path: String, val ok: Boolean = false, val error: String = "", val code: Int = 0, @SerialName("trash_id") val trashId: String = "")

@Serializable
data class UploadStart(val id: String = "", val offset: Long = 0, val size: Long = 0, val skipped: Boolean = false, val path: String = "", val mtime: Long = 0)

@Serializable
data class UploadDone(val path: String = "", val name: String = "", val size: Long = 0, val mtime: Long = 0, val sha256: String = "", val skipped: Boolean = false)

@Serializable
data class SyncEntry(
    @SerialName("p") val path: String,
    @SerialName("d") val dir: Boolean = false,
    @SerialName("s") val size: Long = 0,
    @SerialName("m") val mtime: Long = 0,
    @SerialName("h") val hash: String = "",
)

@Serializable
data class SyncListing(
    val path: String,
    val cursor: Long,
    val entries: List<SyncEntry>,
    val excludes: List<String> = emptyList(),
    val writable: Boolean = false,
)

@Serializable
data class SyncChanged(val changed: Boolean, val cursor: Long, val watching: Boolean = true)

@Serializable
data class FileHash(val path: String, val size: Long, val mtime: Long, val sha256: String)

@Serializable
data class UserSettings(
    val user: String = "",
    val admin: Boolean = false,
    @SerialName("public_url") val publicUrl: String = "",
    @SerialName("default_expiry_hours") val defaultExpiryHours: Double = 168.0,
)

/** What a new link should be. Zero values mean the server's defaults. */
@Serializable
data class ShareInput(
    val path: String,
    val label: String = "",
    val note: String = "",
    @SerialName("expires_hours") val expiresHours: Double = 0.0,
    @SerialName("expires_at") val expiresAt: Long = 0,
    val password: String = "",
    @SerialName("max_downloads") val maxDownloads: Long = 0,
    val slug: String = "",
)

/** The API said no: [status] is its HTTP status, [message] its explanation. */
class ApiException(val status: Int, message: String, val body: String = "") : Exception(message)

/** Conflict details from an upload that would replace something that changed. */
@Serializable
data class Conflict(val error: String = "", val conflict: Boolean = false, val exists: Boolean = false, val size: Long = -1, val mtime: Long = -1, val offset: Long = -1)

@Serializable
data class LinkToken(val token: String = "", val name: String = "", val pin: String = "")

/** A one-use invite to Burrow's tethersys bridge, and the bridge's id. The invite is a credential. */
@Serializable
data class TetherInvite(val id: String = "", val invite: String = "")

@Serializable
data class RemoteView(val id: Long = 0, val name: String = "", val error: String = "")

/** Typed access to Burrow's API, through the encrypted channel. */
class BurrowApi(val channel: BurrowChannel) {
    private suspend fun <T> get(path: String, ser: KSerializer<T>): T = withContext(ioDispatcher) { decode(channel.call("GET", path), ser) }

    private suspend fun <T> send(method: String, path: String, body: JsonObject?, ser: KSerializer<T>): T = withContext(ioDispatcher) {
        decode(channel.call(method, path, body = body?.let { RequestBody.json(it.toString()) }), ser)
    }

    private fun <T> decode(r: Response, ser: KSerializer<T>): T {
        val text = r.text()
        if (!r.ok) throw ApiException(r.status, errorText(text.encodeToByteArray(), "Burrow answered ${r.status}."), text)
        return json.decodeFromString(ser, text)
    }

    private fun q(vararg kv: Pair<String, Any?>) = kv.filter { it.second != null && it.second != "" }
        .joinToString("&") { "${it.first}=${percentEncode(it.second.toString())}" }

    suspend fun me(): ServerInfo = get("/api/device/me", ServerInfo.serializer())

    suspend fun updateMe(name: String? = null, model: String? = null, appVersion: String? = null): ServerInfo = send("PATCH", "/api/device/me", buildJsonObject {
        name?.let { put("name", it) }
        model?.let { put("model", it) }
        appVersion?.let { put("app_version", it) }
    }, ServerInfo.serializer())

    /** Removes this device from Burrow. */
    suspend fun unpair() {
        channel.call("DELETE", "/api/device/me").close()
    }

    suspend fun settings(): UserSettings = get("/api/settings", UserSettings.serializer())

    /** A REST-only token for this account, for linking this Burrow to another one ("Link to my NAS"). */
    suspend fun linkToken(): LinkToken = send("POST", "/api/device/link-token", buildJsonObject { }, LinkToken.serializer())

    /** An invite to Burrow's tethersys bridge for this phone ("Reach from anywhere" must be on). */
    suspend fun tetherInvite(): TetherInvite = send("POST", "/api/device/tether", buildJsonObject { }, TetherInvite.serializer())

    /** Adds a connected NAS to this Burrow (admins); [config] is what Settings, Connected NAS takes. */
    suspend fun addRemote(config: JsonObject): RemoteView = send("POST", "/api/remotes", config, RemoteView.serializer())

    suspend fun ls(path: String): Listing = get("/api/ls?" + q("path" to path), Listing.serializer())

    suspend fun info(path: String): Info = get("/api/info?" + q("path" to path), Info.serializer())

    suspend fun search(text: String, kind: String? = null, root: String? = null, sort: String? = null, limit: Int = 200): SearchResult =
        get("/api/search?" + q("q" to text, "kind" to kind, "root" to root, "sort" to sort, "limit" to limit), SearchResult.serializer())

    suspend fun favorites(): List<Entry> = get("/api/favorites", ListSerializer(Entry.serializer()))

    suspend fun setFavorite(path: String, on: Boolean) {
        val r = if (on) channel.call("POST", "/api/favorites", body = RequestBody.json(buildJsonObject { put("path", path) }.toString()))
        else channel.call("DELETE", "/api/favorites?" + q("path" to path))
        decode(r, ErrorBody.serializer())
    }

    /** A file's bytes, optionally from [from] on (to resume a download). */
    suspend fun raw(path: String, from: Long = 0, ifRange: String? = null): Response {
        val h = mutableMapOf<String, String>()
        if (from > 0) h["Range"] = "bytes=$from-"
        if (from > 0 && ifRange != null) h["If-Range"] = ifRange
        val r = channel.call("GET", "/api/raw?" + q("path" to path), h)
        if (!r.ok) throw ApiException(r.status, errorText(r.text().encodeToByteArray(), "Couldn't open the file (${r.status})."))
        return r
    }

    /** A small JPEG preview, or null when there is none. */
    suspend fun thumb(path: String): ByteArray? {
        val r = channel.call("GET", "/api/thumb?" + q("path" to path))
        return r.use { if (it.ok) it.body.readByteArray() else null }
    }

    suspend fun shares(path: String? = null): List<Share> = get("/api/shares?" + q("path" to path), ListSerializer(Share.serializer()))

    suspend fun createShare(input: ShareInput): Share =
        decode(channel.call("POST", "/api/shares", body = RequestBody.json(json.encodeToString(ShareInput.serializer(), input))), Share.serializer())

    suspend fun setShareActive(id: String, active: Boolean): Share =
        send("PATCH", "/api/shares/$id", buildJsonObject { put("revoked", !active) }, Share.serializer())

    suspend fun deleteShare(id: String) {
        decode(channel.call("DELETE", "/api/shares/$id"), ErrorBody.serializer())
    }

    suspend fun mkdir(path: String, parents: Boolean = false): Entry =
        send("POST", "/api/fs/mkdir", buildJsonObject { put("path", path); put("parents", parents) }, Entry.serializer())

    /** Moves or renames; returns the new path. */
    suspend fun move(from: String, to: String): String =
        send("POST", "/api/fs/move", buildJsonObject { put("from", from); put("to", to) }, MovedTo.serializer()).path

    suspend fun delete(paths: List<String>): List<DeleteResult> =
        send("POST", "/api/fs/delete", buildJsonObject { putJsonArray("paths") { paths.forEach { add(it) } } }, ListSerializer(DeleteResult.serializer()))

    /** Deletes a file only if it is still the version seen (size and mtime in ms). */
    suspend fun deleteIfUnchanged(path: String, size: Long, mtime: Long): DeleteResult =
        send("POST", "/api/fs/delete", buildJsonObject {
            put("items", kotlinx.serialization.json.buildJsonArray {
                add(buildJsonObject { put("path", path); put("size", size); put("mtime", mtime) })
            })
        }, ListSerializer(DeleteResult.serializer())).first()

    suspend fun trash(): List<TrashItem> = get("/api/trash", ListSerializer(TrashItem.serializer()))

    suspend fun restore(id: String): String = send("POST", "/api/trash/$id/restore", null, MovedTo.serializer()).path

    suspend fun purge(id: String) {
        decode(channel.call("DELETE", "/api/trash/$id"), ErrorBody.serializer())
    }

    // ---- uploads ----

    suspend fun startUpload(
        dir: String, name: String, size: Long, mtime: Long, sha256: String = "",
        conflict: String = "rename", expectSize: Long? = null, expectMtime: Long? = null,
    ): UploadStart = send("POST", "/api/uploads", buildJsonObject {
        put("dir", dir); put("name", name); put("size", size); put("mtime", mtime); put("conflict", conflict)
        if (sha256.isNotEmpty()) put("sha256", sha256)
        if (expectSize != null && expectMtime != null) { put("expect_size", expectSize); put("expect_mtime", expectMtime) }
    }, UploadStart.serializer())

    suspend fun uploadOffset(id: String): Long = get("/api/uploads/$id", UploadStart.serializer()).offset

    /** Sends [length] bytes at [offset]; returns the new offset. A 409 carries the server's offset. */
    suspend fun putChunk(id: String, offset: Long, length: Long, write: (BufferedSink) -> Unit): Long =
        decode(channel.call("PUT", "/api/uploads/$id?offset=$offset", body = RequestBody(length, "application/octet-stream", write)), UploadStart.serializer()).offset

    suspend fun finishUpload(id: String, sha256: String = "", conflict: String? = null): UploadDone =
        send("POST", "/api/uploads/$id/finish", buildJsonObject {
            if (sha256.isNotEmpty()) put("sha256", sha256)
            conflict?.let { put("conflict", it) }
        }, UploadDone.serializer())

    suspend fun cancelUpload(id: String) {
        channel.call("DELETE", "/api/uploads/$id").close()
    }

    // ---- sync ----

    suspend fun syncList(path: String): SyncListing = get("/api/sync/list?" + q("path" to path), SyncListing.serializer())

    suspend fun syncChanged(path: String, since: Long): SyncChanged = get("/api/sync/changed?" + q("path" to path, "since" to since), SyncChanged.serializer())

    suspend fun syncHash(path: String): FileHash = get("/api/sync/hash?" + q("path" to path), FileHash.serializer())

    fun conflictOf(e: ApiException): Conflict? =
        if (e.status == 409) runCatching { json.decodeFromString(Conflict.serializer(), e.body) }.getOrNull() else null
}

@Serializable
internal data class MovedTo(val path: String)
