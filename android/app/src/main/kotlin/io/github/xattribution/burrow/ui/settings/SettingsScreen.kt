@file:OptIn(ExperimentalMaterial3Api::class)

package io.github.xattribution.burrow.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.Hub
import androidx.compose.material.icons.outlined.PhoneAndroid
import io.github.xattribution.burrow.ui.files.rememberPhoneAccessRequest
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.Public
import androidx.compose.material.icons.outlined.RestoreFromTrash
import androidx.compose.material.icons.outlined.SwapVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.xattribution.burrow.BuildConfig
import io.github.xattribution.burrow.core.TrashItem
import io.github.xattribution.burrow.data.Link
import io.github.xattribution.burrow.data.RoutePreference
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.data.Place
import io.github.xattribution.burrow.work.unplace
import androidx.compose.material.icons.outlined.Lan
import io.github.xattribution.burrow.ui.common.Load
import io.github.xattribution.burrow.ui.common.formatSize
import io.github.xattribution.burrow.ui.common.formatWhen
import io.github.xattribution.burrow.ui.common.friendly
import kotlinx.coroutines.launch
import uniffi.tethersys_ffi.FfiLink

@Composable
fun SettingsScreen(pairLink: String?, onLinkUsed: () -> Unit) {
    val g = LocalContext.current.graph
    val scope = rememberCoroutineScope()
    val paired by g.device.state.collectAsState()
    val status by g.connection.status.collectAsState()
    val transfersChanged by g.transfers.changed.collectAsState()
    val transfers = remember(transfersChanged) { g.db.transferQueries.recent(20).executeAsList() }
    var trash by remember { mutableStateOf<Load<List<TrashItem>>>(Load.Loading) }
    var trashTick by remember { mutableStateOf(0) }
    var unpairing by remember { mutableStateOf(false) }
    var renaming by remember { mutableStateOf(false) }
    var message by remember { mutableStateOf<String?>(null) }
    var addingPlace by remember { mutableStateOf(false) }
    val places by g.places.list.collectAsState()
    val tetherLinks by g.tether.links.collectAsState()
    var phoneAccess by remember { mutableStateOf(g.phoneAccess()) }
    val requestAccess = rememberPhoneAccessRequest { phoneAccess = it; if (it) g.places.refreshVolumes() }
    androidx.lifecycle.compose.LifecycleResumeEffect(Unit) {
        phoneAccess = g.phoneAccess()
        onPauseOrDispose { }
    }
    val p = paired ?: return
    val tetherLink = tetherLinks[p.info.tether]
    if (addingPlace) AddConnectionDialog(onDismiss = { addingPlace = false }, onAdded = { pl -> addingPlace = false; message = "Connected to ${pl.name}. Pick it at the top of Files." })

    LaunchedEffect(trashTick) {
        trash = try {
            Load.Ready(g.connection.use { it.trash() })
        } catch (e: Exception) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            Load.Failed(friendly(e))
        }
    }
    val snack = remember { androidx.compose.material3.SnackbarHostState() }
    LaunchedEffect(message) { message?.let { snack.showSnackbar(it) } }
    Scaffold(topBar = { TopAppBar(title = { Text("Settings") }) }, snackbarHost = { androidx.compose.material3.SnackbarHost(snack) }) { pad ->
        Box(Modifier.padding(pad).fillMaxSize(), contentAlignment = Alignment.TopCenter) {
            LazyColumn(Modifier.widthIn(max = 720.dp).fillMaxWidth()) {
                item {
                    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        Card(Modifier.fillMaxWidth()) {
                            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                val (icon, text) = when (val s = status) {
                                    is Link.Online -> when {
                                        s.tethered -> Icons.Outlined.Hub to when (tetherLink) {
                                            FfiLink.DIRECT -> "Connected over tethersys, directly"
                                            FfiLink.RELAYED -> "Connected over tethersys, through a relay"
                                            else -> "Connected over tethersys"
                                        }
                                        s.atHome -> Icons.Outlined.Home to "Connected at home"
                                        else -> Icons.Outlined.Public to "Connected from outside"
                                    }
                                    is Link.Offline -> Icons.Outlined.CloudOff to s.message
                                    is Link.Disabled -> Icons.Outlined.CloudOff to s.message
                                    else -> Icons.Outlined.Lock to "Not connected yet"
                                }
                                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                                    Icon(icon, null, tint = MaterialTheme.colorScheme.primary)
                                    Text(text, style = MaterialTheme.typography.titleMedium)
                                }
                                Text("Signed in as ${p.info.user}${if (p.info.admin) " (admin)" else ""}. This phone acts as that account, with its folder access.", style = MaterialTheme.typography.bodyMedium)
                                Row(verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                    Icon(Icons.Outlined.Lock, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.primary)
                                    Text(
                                        "End-to-end encrypted: keys from ML-KEM-768 and X25519 (post-quantum hybrid) plus a secret from pairing, AES-256-GCM for every request. A reverse proxy sees only scrambled data.",
                                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            }
                        }
                        Text("Addresses", style = MaterialTheme.typography.titleSmall)
                        Text("At home: ${p.home ?: "not set"}", style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace))
                        Text("Away: ${p.away ?: "not set"}", style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace))
                        if (p.info.tether.isNotEmpty()) {
                            Text(
                                "Anywhere: tethersys ${p.info.tether.take(10)}" + if (!g.tether.usable) " (not available on this phone)" else "",
                                style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                            )
                        }
                        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                            val opts = listOf(RoutePreference.AUTO to "Automatic", RoutePreference.HOME to "Home only", RoutePreference.AWAY to "Away only")
                            opts.forEachIndexed { i, (v, l) ->
                                SegmentedButton(selected = p.preference == v, onClick = { g.device.setPreference(v) }, shape = SegmentedButtonDefaults.itemShape(i, opts.size)) { Text(l) }
                            }
                        }
                        Text(
                            "Automatic uses the home address on Wi-Fi and the public one elsewhere. Trying the home address on someone else's Wi-Fi is safe: nothing reusable is sent, and only your Burrow can answer.",
                            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                item { HorizontalDivider() }
                item {
                    ListItem(
                        headlineContent = { Text("This phone") },
                        supportingContent = { Text(p.info.deviceName.ifBlank { android.os.Build.MODEL }) },
                        trailingContent = { TextButton(onClick = { renaming = true }) { Text("Rename") } },
                    )
                }
                item { HorizontalDivider() }
                item {
                    Row(Modifier.fillMaxWidth().padding(start = 16.dp, end = 8.dp, top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                        Icon(Icons.Outlined.Lan, null)
                        Text("Connections", style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(start = 12.dp).weight(1f))
                        TextButton(onClick = { addingPlace = true }) { Text("Add") }
                    }
                }
                val others = places.filter { it.id != Place.HOME && it !is Place.Phone }
                if (others.isEmpty()) item {
                    Text("Other Burrows and SMB shares you add stay on this phone.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp))
                }
                items(others, key = { "p${it.id}" }) { pl -> ConnectionRow(pl, homeIsAdmin = p.info.admin) { message = it } }
                item {
                    ListItem(
                        colors = androidx.compose.material3.ListItemDefaults.colors(containerColor = androidx.compose.ui.graphics.Color.Transparent),
                        leadingContent = { Icon(Icons.Outlined.PhoneAndroid, null, tint = MaterialTheme.colorScheme.primary) },
                        headlineContent = { Text("Files on this phone") },
                        supportingContent = { Text(if (phoneAccess) "Every folder, memory cards and USB drives" else "Off") },
                        trailingContent = if (phoneAccess) null else ({ TextButton(onClick = requestAccess) { Text("Allow") } }),
                    )
                }
                if (transfers.isNotEmpty()) {
                    item { HorizontalDivider() }
                    item {
                        Row(Modifier.fillMaxWidth().padding(start = 16.dp, end = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                            Icon(Icons.Outlined.SwapVert, null)
                            Text("Uploads and downloads", style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(start = 12.dp).weight(1f))
                            TextButton(onClick = { g.db.transferQueries.clearDone(); g.transfers.changed.value++ }) { Text("Clear finished") }
                        }
                    }
                    items(transfers, key = { "t${it.id}" }) { t ->
                        ListItem(
                            headlineContent = { Text(t.name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                            supportingContent = {
                                Column {
                                    val (dst, dir) = unplace(t.remote_dir)
                                    val placeName = g.places.get(dst)?.name ?: "Burrow"
                                    val where = when (t.direction) {
                                        "down" -> "to Downloads/Burrow"
                                        else -> "to $placeName" + if (dir.isNotEmpty()) "/$dir" else ""
                                    }
                                    val (doing, did) = when (t.direction) {
                                        "up" -> "Sending" to "Sent"
                                        "copy" -> "Copying" to "Copied"
                                        "move" -> "Moving" to "Moved"
                                        else -> "Saving" to "Saved"
                                    }
                                    Text(when (t.state) {
                                        "done" -> "$did $where"
                                        "failed" -> "Failed: ${t.result}"
                                        "cancelled" -> "Cancelled"
                                        else -> "$doing $where" + if (t.size > 0) ", ${formatSize(t.done_bytes)} of ${formatSize(t.size)}" else ""
                                    }, maxLines = 2)
                                    if (t.state == "running" || t.state == "queued") LinearProgressIndicator(progress = { if (t.size > 0) t.done_bytes.toFloat() / t.size else 0f }, modifier = Modifier.fillMaxWidth().padding(top = 4.dp))
                                }
                            },
                        )
                    }
                    item { HorizontalDivider() }
                }
                item {
                    Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
                        Icon(Icons.Outlined.Delete, null)
                        Text("Recently deleted on Burrow", style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(start = 12.dp))
                    }
                }
                when (val t = trash) {
                    is Load.Loading -> item { LinearProgressIndicator(Modifier.fillMaxWidth().padding(16.dp)) }
                    is Load.Failed -> item { Text(t.message, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(horizontal = 16.dp)) }
                    is Load.Ready -> {
                        if (t.value.isEmpty()) item { Text("Nothing you deleted in the last 30 days.", style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(horizontal = 16.dp)) }
                        items(t.value.take(100), key = { "d${it.id}" }) { item ->
                            ListItem(
                                headlineContent = { Text(item.name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                                supportingContent = { Text("${item.path.substringBeforeLast('/', "")} · deleted ${formatWhen(item.deleted)}", maxLines = 1, overflow = TextOverflow.Ellipsis) },
                                trailingContent = {
                                    IconButton(onClick = {
                                        scope.launch {
                                            message = try {
                                                "Restored to " + g.connection.use { it.restore(item.id) }
                                            } catch (e: Exception) {
                                                friendly(e)
                                            }
                                            trashTick++
                                        }
                                    }) { Icon(Icons.Outlined.RestoreFromTrash, "Restore") }
                                },
                            )
                        }
                    }
                }
                item {
                    message?.let { Text(it, modifier = Modifier.padding(16.dp), color = MaterialTheme.colorScheme.primary) }
                    HorizontalDivider(Modifier.padding(top = 8.dp))
                    ListItem(
                        headlineContent = { Text("Remove this phone from Burrow", color = MaterialTheme.colorScheme.error) },
                        supportingContent = { Text("Deletes this phone's key and stops all syncing. Files stay where they are.") },
                        modifier = Modifier.padding(bottom = 8.dp),
                        trailingContent = { TextButton(onClick = { unpairing = true }) { Text("Remove") } },
                    )
                    Text(
                        "Burrow ${BuildConfig.VERSION_NAME}. Open source; nothing is sent anywhere but your own server.",
                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(16.dp),
                    )
                }
            }
        }
    }
    if (pairLink != null) {
        AlertDialog(
            onDismissRequest = onLinkUsed,
            title = { Text("Already paired") },
            text = { Text("This phone is paired with ${p.away ?: p.home}. To pair it with a different Burrow, remove it here first, then scan the code again.") },
            confirmButton = { TextButton(onClick = onLinkUsed) { Text("OK") } },
        )
    }
    if (renaming) {
        var n by remember { mutableStateOf(p.info.deviceName) }
        AlertDialog(
            onDismissRequest = { renaming = false },
            title = { Text("Name this phone") },
            text = { OutlinedTextField(n, { n = it }, singleLine = true) },
            confirmButton = {
                TextButton(enabled = n.isNotBlank(), onClick = {
                    renaming = false
                    scope.launch { runCatching { g.connection.use { api -> g.device.updateInfo(api.updateMe(name = n.trim())) } } }
                }) { Text("Save") }
            },
            dismissButton = { TextButton(onClick = { renaming = false }) { Text("Cancel") } },
        )
    }
    if (unpairing) {
        AlertDialog(
            onDismissRequest = { unpairing = false },
            title = { Text("Remove this phone?") },
            text = { Text("It will no longer reach Burrow, and folder syncing stops. Nothing is deleted. You can pair it again later.") },
            confirmButton = {
                TextButton(onClick = {
                    unpairing = false
                    scope.launch {
                        g.db.pairQueries.all().executeAsList().forEach { g.scheduler.cancel(it.id); g.db.pairQueries.remove(it.id) }
                        g.scheduler.reschedule()
                        g.connection.unpair()
                    }
                }) { Text("Remove") }
            },
            dismissButton = { TextButton(onClick = { unpairing = false }) { Text("Cancel") } },
        )
    }
}
