@file:OptIn(ExperimentalMaterial3Api::class, ExperimentalMaterial3AdaptiveApi::class)

package io.github.xattribution.burrow.ui.sync

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.DocumentsContract
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Backup
import androidx.compose.material.icons.outlined.CloudDownload
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.PhotoLibrary
import androidx.compose.material.icons.outlined.Sync
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.adaptive.ExperimentalMaterial3AdaptiveApi
import androidx.compose.material3.adaptive.layout.AnimatedPane
import androidx.compose.material3.adaptive.layout.ListDetailPaneScaffoldRole
import androidx.compose.material3.adaptive.layout.PaneAdaptedValue
import androidx.compose.material3.adaptive.navigation.NavigableListDetailPaneScaffold
import androidx.compose.material3.adaptive.navigation.rememberListDetailPaneScaffoldNavigator
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import io.github.xattribution.burrow.core.SyncMode
import io.github.xattribution.burrow.core.db.Pair as PairRow
import io.github.xattribution.burrow.data.MediaTree
import io.github.xattribution.burrow.data.Place
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.ui.common.BurrowFolderPicker
import io.github.xattribution.burrow.ui.common.EmptyState
import io.github.xattribution.burrow.ui.common.formatSize
import io.github.xattribution.burrow.ui.common.formatWhen
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

fun SyncMode.title() = when (this) {
    SyncMode.BACKUP -> "Back up to Burrow"
    SyncMode.TWO_WAY -> "Keep in sync both ways"
    SyncMode.DOWNLOAD -> "Keep a copy on this phone"
}

fun SyncMode.explain() = when (this) {
    SyncMode.BACKUP -> "New and changed files go to Burrow. Deleting on the phone deletes nothing on Burrow."
    SyncMode.TWO_WAY -> "Additions, changes and deletes go both ways. Deleted files wait in Burrow's trash for 30 days."
    SyncMode.DOWNLOAD -> "The Burrow folder is copied to the phone and kept up to date. Edits on the phone are kept as copies."
}

private fun SyncMode.icon() = when (this) {
    SyncMode.BACKUP -> Icons.Outlined.Backup
    SyncMode.TWO_WAY -> Icons.Outlined.Sync
    SyncMode.DOWNLOAD -> Icons.Outlined.CloudDownload
}

/** Folder pairs: list on one side, the selected pair's details on the other when there's room. */
@Composable
fun SyncScreen() {
    val g = LocalContext.current.graph
    val navigator = rememberListDetailPaneScaffoldNavigator<Long>()
    val scope = rememberCoroutineScope()
    var tick by remember { mutableIntStateOf(0) }
    val pairs = remember(tick) { g.db.pairQueries.all().executeAsList() }
    val live by g.scheduler.live.collectAsState()
    var adding by rememberSaveable { mutableStateOf(false) }
    // pairs' status changes in the background; look again now and then
    LaunchedEffect(Unit) { while (true) { delay(3000); tick++ } }
    val twoPane = navigator.scaffoldValue[ListDetailPaneScaffoldRole.List] == PaneAdaptedValue.Expanded &&
        navigator.scaffoldValue[ListDetailPaneScaffoldRole.Detail] == PaneAdaptedValue.Expanded

    NavigableListDetailPaneScaffold(
        navigator = navigator,
        listPane = {
            AnimatedPane {
                Scaffold(
                    topBar = { TopAppBar(title = { Text("Sync and backup") }) },
                    floatingActionButton = { ExtendedFloatingActionButton(onClick = { adding = true }, icon = { Icon(Icons.Outlined.Add, null) }, text = { Text("Add a folder") }) },
                ) { pad ->
                    Box(Modifier.padding(pad).fillMaxSize()) {
                        if (pairs.isEmpty()) {
                            EmptyState(
                                Icons.Outlined.Backup, "Nothing syncs yet",
                                "Back up your photos, keep a folder in sync with Burrow both ways, or keep a copy of a Burrow folder on this phone.",
                                action = "Add a folder",
                            ) { adding = true }
                        } else LazyColumn(Modifier.fillMaxSize()) {
                            items(pairs, key = { it.id }) { p ->
                                val running = live?.takeIf { it.pairId == p.id }
                                val sel = twoPane && navigator.currentDestination?.contentKey == p.id
                                ListItem(
                                    modifier = Modifier.clickable { scope.launch { navigator.navigateTo(ListDetailPaneScaffoldRole.Detail, p.id) } },
                                    leadingContent = { Icon(SyncMode.of(p.mode).icon(), null, tint = MaterialTheme.colorScheme.primary) },
                                    headlineContent = { Text(p.name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                                    supportingContent = {
                                        Column {
                                            Text("${p.local_label} → ${p.remote}", maxLines = 1, overflow = TextOverflow.Ellipsis)
                                            Text(
                                                when {
                                                    running != null -> running.phase + if (running.total > 0) " ${running.done + 1} of ${running.total}" else ""
                                                    p.enabled == 0L -> "Paused"
                                                    p.last_error.isNotEmpty() -> p.last_error
                                                    p.last_sync > 0 -> "Synced ${formatWhen(p.last_sync)}"
                                                    else -> "Not synced yet"
                                                },
                                                color = if (p.last_error.isNotEmpty() && running == null) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                                                maxLines = 1, overflow = TextOverflow.Ellipsis,
                                            )
                                            if (running != null) LinearProgressIndicator(progress = { if (running.totalBytes > 0) running.bytes.toFloat() / running.totalBytes else 0f }, modifier = Modifier.fillMaxWidth().padding(top = 4.dp))
                                        }
                                    },
                                    tonalElevation = if (sel) 4.dp else 0.dp,
                                )
                            }
                        }
                    }
                }
            }
        },
        detailPane = {
            AnimatedPane {
                val id = navigator.currentDestination?.contentKey
                val p = pairs.firstOrNull { it.id == id }
                if (p == null) EmptyState(Icons.Outlined.Sync, "Choose a folder pair", "Its activity and settings show here.")
                else PairDetail(p, showBack = !twoPane, onBack = { scope.launch { navigator.navigateBack() } }, onChanged = { tick++ }, onRemoved = { tick++; scope.launch { navigator.navigateBack() } })
            }
        },
    )
    if (adding) AddPairDialog(onDone = { id -> adding = false; tick++; if (id != null) scope.launch { navigator.navigateTo(ListDetailPaneScaffoldRole.Detail, id) } })
}

@Composable
private fun PairDetail(p: PairRow, showBack: Boolean, onBack: () -> Unit, onChanged: () -> Unit, onRemoved: () -> Unit) {
    val g = LocalContext.current.graph
    val live by g.scheduler.live.collectAsState()
    var removing by remember { mutableStateOf(false) }
    val log = remember(p.last_sync, live) { g.db.pairQueries.recentLog(p.id, 100).executeAsList() }
    val tracked = remember(p.last_sync) { g.db.pairQueries.countBase(p.id).executeAsOne() }
    fun save(enabled: Long = p.enabled, wifi: Long = p.wifi_only, charging: Long = p.charging_only) {
        g.db.pairQueries.update(p.name, p.mode, wifi, charging, enabled, p.id)
        g.scheduler.reschedule()
        onChanged()
    }
    Scaffold(topBar = {
        TopAppBar(
            navigationIcon = { if (showBack) IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            title = { Text(p.name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        )
    }) { pad ->
        LazyColumn(Modifier.padding(pad).fillMaxSize()) {
            item {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    val mode = SyncMode.of(p.mode)
                    Card(Modifier.fillMaxWidth()) {
                        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            Text(mode.title(), style = MaterialTheme.typography.titleMedium)
                            Text(mode.explain(), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            HorizontalDivider(Modifier.padding(vertical = 4.dp))
                            Text("On the phone: ${p.local_label}", style = MaterialTheme.typography.bodyMedium)
                            Text("On Burrow: ${p.remote}", style = MaterialTheme.typography.bodyMedium)
                            Text("$tracked files in sync", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                    val running = live?.takeIf { it.pairId == p.id }
                    if (running != null) {
                        Text("${running.phase}${if (running.total > 0) " ${running.done + 1} of ${running.total}" else ""}: ${running.current.substringAfterLast('/')}", maxLines = 1, overflow = TextOverflow.Ellipsis)
                        LinearProgressIndicator(progress = { if (running.totalBytes > 0) running.bytes.toFloat() / running.totalBytes else 0f }, modifier = Modifier.fillMaxWidth())
                        if (running.totalBytes > 0) Text("${formatSize(running.bytes)} of ${formatSize(running.totalBytes)}", style = MaterialTheme.typography.bodySmall)
                    } else {
                        Text(if (p.last_sync > 0) "Last synced ${formatWhen(p.last_sync)}" else "Not synced yet", style = MaterialTheme.typography.bodyMedium)
                        if (p.last_error.isNotEmpty()) Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            Icon(Icons.Outlined.ErrorOutline, null, tint = MaterialTheme.colorScheme.error)
                            Text(p.last_error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                        }
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Button(enabled = running == null && p.enabled != 0L, onClick = { g.scheduler.syncNow(p.id) }) { Text("Sync now") }
                        OutlinedButton(onClick = { save(enabled = if (p.enabled != 0L) 0 else 1) }) { Text(if (p.enabled != 0L) "Pause" else "Resume") }
                    }
                    Toggle("Only on Wi-Fi", "Waits for a network that isn't metered.", p.wifi_only != 0L) { save(wifi = if (it) 1 else 0) }
                    Toggle("Only while charging", null, p.charging_only != 0L) { save(charging = if (it) 1 else 0) }
                    TextButton(onClick = { removing = true }) { Text("Stop syncing this folder", color = MaterialTheme.colorScheme.error) }
                    if (log.isNotEmpty()) Text("Recent activity", style = MaterialTheme.typography.titleSmall)
                }
            }
            items(log, key = { it.id }) { e ->
                ListItem(
                    headlineContent = { Text(e.path.substringAfterLast('/'), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                    supportingContent = { Text(listOf(e.kind.replaceFirstChar { it.uppercase() }, e.detail, formatWhen(e.ts)).filter { it.isNotEmpty() }.joinToString(" · "), maxLines = 2) },
                )
            }
        }
    }
    if (removing) {
        AlertDialog(
            onDismissRequest = { removing = false },
            title = { Text("Stop syncing ${p.name}?") },
            text = { Text("Nothing is deleted on the phone or on Burrow; the two just stop being kept in step.") },
            confirmButton = {
                TextButton(onClick = {
                    removing = false
                    g.scheduler.cancel(p.id)
                    g.db.pairQueries.remove(p.id)
                    if (!p.local_uri.startsWith(MediaTree.PREFIX)) runCatching { g.app.contentResolver.releasePersistableUriPermission(Uri.parse(p.local_uri), Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION) }
                    g.scheduler.reschedule()
                    onRemoved()
                }) { Text("Stop syncing") }
            },
            dismissButton = { TextButton(onClick = { removing = false }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun Toggle(title: String, body: String?, checked: Boolean, onChange: (Boolean) -> Unit) {
    ListItem(
        modifier = Modifier.clickable { onChange(!checked) },
        headlineContent = { Text(title) },
        supportingContent = body?.let { { Text(it) } },
        trailingContent = { Switch(checked = checked, onCheckedChange = onChange) },
    )
}

/** Adding a pair: what on the phone, how, where on Burrow, and when. */
@Composable
private fun AddPairDialog(onDone: (Long?) -> Unit) {
    val ctx = LocalContext.current
    val g = ctx.graph
    var step by rememberSaveable { mutableIntStateOf(0) }
    var localUri by rememberSaveable { mutableStateOf<String?>(null) }
    var localLabel by rememberSaveable { mutableStateOf("") }
    var mode by rememberSaveable { mutableStateOf(SyncMode.BACKUP) }
    var remote by rememberSaveable { mutableStateOf<String?>(null) }
    var wifi by rememberSaveable { mutableStateOf(true) }
    var charging by rememberSaveable { mutableStateOf(false) }
    var name by rememberSaveable { mutableStateOf("") }
    val photos = localUri?.startsWith(MediaTree.PREFIX) == true

    val treePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri: Uri? ->
        if (uri != null) {
            ctx.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
            localUri = uri.toString()
            localLabel = DocumentsContract.getTreeDocumentId(uri).substringAfter(':').ifEmpty { "Phone storage" }
            name = localLabel.substringAfterLast('/')
            step = 1
        }
    }
    val mediaPerms = buildList {
        if (Build.VERSION.SDK_INT >= 33) { add(Manifest.permission.READ_MEDIA_IMAGES); add(Manifest.permission.READ_MEDIA_VIDEO) } else add(Manifest.permission.READ_EXTERNAL_STORAGE)
        add(Manifest.permission.ACCESS_MEDIA_LOCATION)
    }.toTypedArray()
    var permError by remember { mutableStateOf<String?>(null) }
    val askMedia = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { res ->
        val full = if (Build.VERSION.SDK_INT >= 33) res[Manifest.permission.READ_MEDIA_IMAGES] == true else res[Manifest.permission.READ_EXTERNAL_STORAGE] == true
        if (full) {
            localUri = MediaTree.ALL
            localLabel = "Photos and videos"
            name = "Photos"
            mode = SyncMode.BACKUP
            step = 2
        } else permError = "To back up every photo, allow Burrow access to all photos and videos (not just selected ones)."
    }
    val askNotify = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { }
    // any folder on the phone (Downloads, the top of a memory card) needs All files access;
    // without it, Android's own folder picker is the way, and it refuses a few folders
    var pickingPhone by rememberSaveable { mutableStateOf(false) }
    val askFiles = io.github.xattribution.burrow.ui.files.rememberPhoneAccessRequest { ok -> if (ok) pickingPhone = true else treePicker.launch(null) }
    if (pickingPhone) {
        io.github.xattribution.burrow.ui.files.PlaceTargetPicker(
            title = "Which folder on the phone?",
            confirm = "Use this folder",
            start = Place.PHONE,
            only = { it is Place.Phone },
            onDismiss = { pickingPhone = false },
        ) { placeId, dir ->
            pickingPhone = false
            val vol = g.places.get(placeId) as? Place.Phone
            if (vol != null) {
                val f = if (dir.isEmpty()) vol.root else java.io.File(vol.root, dir)
                localUri = Uri.fromFile(f).toString()
                localLabel = if (dir.isEmpty()) vol.name else dir.substringAfterLast('/')
                name = localLabel
                step = 1
            }
        }
        return
    }

    if (step == 2) {
        Dialog(onDismissRequest = { step = if (photos) 0 else 1 }, properties = DialogProperties(usePlatformDefaultWidth = false)) {
            BurrowFolderPicker(
                title = if (mode == SyncMode.DOWNLOAD) "Which Burrow folder?" else "Where on Burrow?",
                needWrite = mode != SyncMode.DOWNLOAD, confirm = "Use this folder",
                onPick = { remote = it; step = 3 }, onCancel = { step = if (photos) 0 else 1 },
            )
        }
        return
    }
    Dialog(onDismissRequest = { onDone(null) }, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Scaffold(topBar = {
            TopAppBar(
                navigationIcon = { IconButton(onClick = { if (step == 0) onDone(null) else step = if (step == 3) 2 else step - 1 }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                title = { Text("Add a folder") },
            )
        }) { pad ->
            Box(Modifier.padding(pad).fillMaxSize(), contentAlignment = Alignment.TopCenter) {
                Column(Modifier.widthIn(max = 640.dp).fillMaxWidth().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    when (step) {
                        0 -> {
                            Text("What on this phone?", style = MaterialTheme.typography.titleLarge)
                            Choice(Icons.Outlined.PhotoLibrary, "Photos and videos", "Every photo and video on the phone, backed up as they're taken. Locations in photos are kept.") { askMedia.launch(mediaPerms) }
                            Choice(Icons.Outlined.Folder, "A folder", "Any folder on the phone, its memory card or a USB drive: back it up, sync it both ways, or fill it from Burrow.") {
                                if (g.phoneAccess()) pickingPhone = true else askFiles()
                            }
                            permError?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                        }
                        1 -> {
                            Text("How should $localLabel sync?", style = MaterialTheme.typography.titleLarge)
                            SyncMode.entries.forEach { m ->
                                Row(
                                    Modifier.fillMaxWidth().selectable(selected = mode == m, onClick = { mode = m }).padding(vertical = 8.dp),
                                    verticalAlignment = Alignment.Top,
                                ) {
                                    RadioButton(selected = mode == m, onClick = { mode = m })
                                    Column(Modifier.padding(start = 8.dp)) {
                                        Text(m.title(), style = MaterialTheme.typography.titleMedium)
                                        Text(m.explain(), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                    }
                                }
                            }
                            Button(onClick = { step = 2 }, modifier = Modifier.align(Alignment.End)) { Text("Next") }
                        }
                        else -> {
                            Text("Almost done", style = MaterialTheme.typography.titleLarge)
                            Text("${mode.title()}: $localLabel ↔ $remote", style = MaterialTheme.typography.bodyMedium)
                            OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                            Toggle("Only on Wi-Fi", "Waits for a network that isn't metered.", wifi) { wifi = it }
                            Toggle("Only while charging", null, charging) { charging = it }
                            Button(modifier = Modifier.fillMaxWidth(), onClick = {
                                val q = g.db.pairQueries
                                q.insert(name.ifBlank { localLabel }, localUri!!, localLabel, remote!!, mode.id, if (wifi) 1 else 0, if (charging) 1 else 0, System.currentTimeMillis() / 1000)
                                val id = q.lastId().executeAsOne()
                                g.scheduler.reschedule()
                                g.scheduler.syncNow(id)
                                if (Build.VERSION.SDK_INT >= 33 && ctx.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) askNotify.launch(Manifest.permission.POST_NOTIFICATIONS)
                                onDone(id)
                            }) { Text("Start syncing") }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun Choice(icon: androidx.compose.ui.graphics.vector.ImageVector, title: String, body: String, onClick: () -> Unit) {
    Card(onClick = onClick, modifier = Modifier.fillMaxWidth()) {
        Row(Modifier.padding(16.dp), horizontalArrangement = Arrangement.spacedBy(16.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(icon, null, Modifier.size(32.dp), tint = MaterialTheme.colorScheme.primary)
            Column {
                Text(title, style = MaterialTheme.typography.titleMedium)
                Text(body, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}
