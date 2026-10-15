@file:OptIn(ExperimentalMaterial3Api::class, ExperimentalMaterial3AdaptiveApi::class, ExperimentalFoundationApi::class)

package io.github.xattribution.burrow.ui.files

import android.content.Intent
import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.draganddrop.dragAndDropSource
import androidx.compose.foundation.draganddrop.dragAndDropTarget
import androidx.compose.foundation.border
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.CreateNewFolder
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.GridView
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.SearchOff
import androidx.compose.material.icons.outlined.Star
import androidx.compose.material.icons.outlined.Upload
import androidx.compose.material.icons.outlined.ViewList
import androidx.compose.material.icons.outlined.WifiOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.LargeTopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowRight
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.heightIn
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.text.font.FontWeight
import io.github.xattribution.burrow.ui.common.formatDate
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
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draganddrop.mimeTypes
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import coil3.compose.AsyncImage
import io.github.xattribution.burrow.core.Entry
import io.github.xattribution.burrow.data.Place
import io.github.xattribution.burrow.ui.common.friendly
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Dns
import androidx.compose.material.icons.outlined.Lan
import androidx.compose.material.icons.outlined.Storage
import androidx.compose.material.icons.outlined.VerticalSplit
import androidx.compose.material.icons.outlined.Checklist
import androidx.compose.material.icons.outlined.FolderOff
import androidx.compose.material.icons.outlined.FolderShared
import androidx.compose.material.icons.outlined.PhoneAndroid
import androidx.compose.material.icons.outlined.SdCard
import androidx.compose.material.icons.outlined.Usb
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.media.ThumbFetcher
import io.github.xattribution.burrow.ui.common.Busy
import io.github.xattribution.burrow.ui.common.EmptyState
import io.github.xattribution.burrow.ui.common.Load
import io.github.xattribution.burrow.ui.common.formatSize
import io.github.xattribution.burrow.ui.common.kindIcon
import kotlinx.coroutines.launch

/** Detail keys: "<place>\u0002<path>" for a file, "\u0001<place>" for a second place shown beside. */
private const val FILE_SEP = '\u0002'
private const val BESIDE = "\u0001"

/**
 * Browse and search Burrow, or another place this phone connects to. On a
 * phone (or a folded foldable) the folder and a file's details take turns on
 * screen; with room for two panes (unfolded, a tablet, landscape) they sit side
 * by side, clear of the hinge, and the second pane can show another place to
 * drag files between the two.
 */
@Composable
fun FilesScreen() {
    val g = LocalContext.current.graph
    val places by g.places.list.collectAsState()
    var placeId by rememberSaveable { mutableStateOf(Place.HOME) }
    if (places.none { it.id == placeId }) placeId = Place.HOME
    val vm: FilesViewModel = viewModel(key = "files:$placeId") { FilesViewModel(g.places.fs(placeId)) }
    val navigator = rememberListDetailPaneScaffoldNavigator<String>()
    val scope = rememberCoroutineScope()
    val twoPane = navigator.scaffoldValue[ListDetailPaneScaffoldRole.List] == PaneAdaptedValue.Expanded &&
        navigator.scaffoldValue[ListDetailPaneScaffoldRole.Detail] == PaneAdaptedValue.Expanded
    val canBeside = navigator.scaffoldDirective.maxHorizontalPartitions > 1 && places.size > 1
    val key = navigator.currentDestination?.contentKey
    val selected = key?.takeIf { twoPane && it.substringBefore(FILE_SEP) == placeId }?.substringAfter(FILE_SEP)
    NavigableListDetailPaneScaffold(
        navigator = navigator,
        listPane = {
            AnimatedPane {
                FolderPane(
                    vm, placeId, places, selected = selected,
                    onPlace = {
                        placeId = it
                        // a file from the old place shouldn't stay open beside the new one
                        if (navigator.currentDestination?.contentKey?.startsWith(BESIDE) == false) scope.launch { navigator.navigateBack() }
                    },
                    onBeside = if (canBeside) ({ scope.launch { navigator.navigateTo(ListDetailPaneScaffoldRole.Detail, BESIDE + (places.firstOrNull { it.id != placeId }?.id ?: Place.HOME)) } }) else null,
                ) { e ->
                    if (e.dir) vm.open(e.path) else scope.launch { navigator.navigateTo(ListDetailPaneScaffoldRole.Detail, "$placeId$FILE_SEP${e.path}") }
                }
            }
        },
        detailPane = {
            AnimatedPane {
                val p = navigator.currentDestination?.contentKey
                when {
                    p == null -> EmptyState(Icons.Outlined.Folder, "Nothing selected", "Choose a file to see it here.")
                    p.startsWith(BESIDE) -> BesidePane(p.removePrefix(BESIDE), places,
                        onPlace = { id -> scope.launch { navigator.navigateTo(ListDetailPaneScaffoldRole.Detail, BESIDE + id) } },
                        onClose = { scope.launch { navigator.navigateBack() } })
                    else -> DetailPane(
                        path = p.substringAfter(FILE_SEP),
                        place = p.substringBefore(FILE_SEP),
                        showBack = !twoPane,
                        onBack = { scope.launch { navigator.navigateBack() } },
                        onChanged = { vm.refresh() },
                        onOpenFolder = { folder -> vm.open(folder); scope.launch { navigator.navigateBack() } },
                        onRenamed = { np -> vm.refresh(); scope.launch { navigator.navigateTo(ListDetailPaneScaffoldRole.Detail, "${p.substringBefore(FILE_SEP)}$FILE_SEP$np") } },
                        onDeleted = { vm.refresh(); scope.launch { navigator.navigateBack() } },
                    )
                }
            }
        },
    )
}

/** A second place next to the first, to drag files between them. Files open in another app. */
@Composable
private fun BesidePane(placeId: String, places: List<Place>, onPlace: (String) -> Unit, onClose: () -> Unit) {
    val ctx = LocalContext.current
    val vm: FilesViewModel = viewModel(key = "beside:$placeId") { FilesViewModel(ctx.graph.places.fs(placeId)) }
    FolderPane(vm, placeId, places, selected = null, onPlace = onPlace, onBeside = null, onClose = onClose) { e ->
        if (e.dir) vm.open(e.path) else runCatching { openWith(ctx, placeId, e) }.onFailure { vm.message.value = friendly(it) }
    }
}

@Composable
private fun FolderPane(
    vm: FilesViewModel,
    placeId: String,
    places: List<Place>,
    selected: String?,
    onPlace: (String) -> Unit,
    onBeside: (() -> Unit)?,
    onClose: (() -> Unit)? = null,
    onOpen: (Entry) -> Unit,
) {
    val ctx = LocalContext.current
    val place = places.firstOrNull { it.id == placeId }
    val isBurrow = vm.fs.isBurrow
    var adding by remember { mutableStateOf(false) }
    val path by vm.path.collectAsState()
    val listing by vm.listing.collectAsState()
    val query by vm.query.collectAsState()
    val results by vm.results.collectAsState()
    val favorites by vm.favorites.collectAsState()
    val grid by vm.grid.collectAsState()
    val message by vm.message.collectAsState()
    val picked by vm.picked.collectAsState()
    val needsAccess by vm.needsAccess.collectAsState()
    val requestAccess = rememberPhoneAccessRequest { if (it) vm.refresh() }
    val snack = remember { SnackbarHostState() }
    var searching by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    var newFolder by remember { mutableStateOf(false) }
    var shareFolder by remember { mutableStateOf(false) }
    val canWrite = (listing as? Load.Ready)?.value?.canWrite == true
    val canShare = (listing as? Load.Ready)?.value?.canShare == true
    // opening a folder from search results leaves search
    val open: (Entry) -> Unit = { e ->
        if (e.dir && searching) {
            searching = false
            vm.search("")
        }
        onOpen(e)
    }

    LaunchedEffect(message) {
        message?.let { snack.showSnackbar(it); vm.message.value = null }
    }
    BackHandler(enabled = picked.isNotEmpty() || searching || path.isNotEmpty()) {
        when {
            picked.isNotEmpty() -> vm.picked.value = emptySet()
            searching -> { searching = false; vm.search("") }
            else -> vm.up()
        }
    }
    // what is on screen, to turn picked paths back into entries
    val shown: List<Entry> = (if (searching) (results as? Load.Ready)?.value?.entries else (listing as? Load.Ready)?.value?.entries).orEmpty()
    val pickedEntries = shown.filter { it.path in picked }
    var acting by remember { mutableStateOf<String?>(null) } // "copy", "move" or "delete" for the picked items
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris: List<Uri> ->
        for (u in uris) {
            runCatching { ctx.contentResolver.takePersistableUriPermission(u, Intent.FLAG_GRANT_READ_URI_PERMISSION) }
            ctx.graph.transfers.upload(u, path, place = placeId)
        }
        if (uris.isNotEmpty()) vm.message.value = "Sending ${uris.size} file${if (uris.size == 1) "" else "s"} to ${path.substringAfterLast('/').ifEmpty { place?.name.orEmpty() }}"
    }

    val scope = rememberCoroutineScope()
    val haptic = LocalHapticFeedback.current
    val dd = remember(placeId) { DragDrop(ctx, scope, vm, placeId) { haptic.performHapticFeedback(HapticFeedbackType.LongPress) } }
    val pickable: (Entry) -> Boolean = { e -> !isLibraryRoot(e, dd) }
    val list: @Composable (List<Entry>, Boolean, Boolean) -> Unit = { entries, asGrid, where ->
        EntryList(
            entries, grid = asGrid, selected = selected, showWhere = where, dd = dd, place = placeId,
            picked = picked, pickedEntries = pickedEntries,
            onPick = { e -> if (pickable(e)) { haptic.performHapticFeedback(HapticFeedbackType.LongPress); vm.pick(e.path) } },
            onOpen = { e -> if (picked.isNotEmpty()) { if (pickable(e)) vm.pick(e.path) } else open(e) },
        )
    }
    val paneTarget = remember(path, canWrite, placeId) { dd.target(path, enabled = canWrite && (path.isNotEmpty() || !isBurrow)) }

    // the folder name starts large and collapses into the bar as the list scrolls, like the system apps
    val collapse = TopAppBarDefaults.exitUntilCollapsedScrollBehavior()
    val barColors = TopAppBarDefaults.topAppBarColors(
        containerColor = MaterialTheme.colorScheme.background,
        scrolledContainerColor = MaterialTheme.colorScheme.background,
    )
    Scaffold(
        modifier = if (searching) Modifier else Modifier.nestedScroll(collapse.nestedScrollConnection),
        containerColor = MaterialTheme.colorScheme.background,
        topBar = {
            if (picked.isNotEmpty()) {
                PickBar(
                    count = picked.size,
                    canChange = canWrite || searching,
                    hasFiles = pickedEntries.any { !it.dir },
                    onClear = { vm.picked.value = emptySet() },
                    onAll = { vm.picked.value = shown.filter(pickable).map { it.path }.toSet() },
                    onSend = { runCatching { sendFiles(ctx, placeId, pickedEntries.filter { !it.dir }) }.onFailure { vm.message.value = friendly(it) } },
                    onCopy = { acting = "copy" },
                    onMove = { acting = "move" },
                    onDelete = { acting = "delete" },
                )
            } else if (searching) {
                TopAppBar(
                    navigationIcon = { IconButton(onClick = { searching = false; vm.search("") }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Close search") } },
                    title = {
                        TextField(
                            value = query, onValueChange = vm::search, singleLine = true,
                            placeholder = { Text(if (path.isEmpty()) "Search everything" else "Search in ${path.substringAfterLast('/')}") },
                            colors = TextFieldDefaults.colors(focusedContainerColor = MaterialTheme.colorScheme.surface, unfocusedContainerColor = MaterialTheme.colorScheme.surface),
                            modifier = Modifier.fillMaxWidth(),
                        )
                    },
                )
            } else {
                LargeTopAppBar(
                    scrollBehavior = collapse,
                    colors = barColors,
                    navigationIcon = {
                        if (path.isNotEmpty()) IconButton(onClick = { vm.up() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Up") }
                        else if (onClose != null) IconButton(onClick = onClose) { Icon(Icons.Outlined.Close, "Close") }
                    },
                    title = {
                        if (path.isEmpty()) PlaceSwitcher(places, placeId, onPlace = onPlace, onAdd = { adding = true })
                        else Text(path.substringAfterLast('/'), maxLines = 1, overflow = TextOverflow.Ellipsis)
                    },
                    actions = {
                        if (isBurrow || vm.fs.canSearch) IconButton(onClick = { searching = true }) { Icon(Icons.Outlined.Search, "Search") }
                        IconButton(onClick = { vm.grid.value = !grid }) { Icon(if (grid) Icons.Outlined.ViewList else Icons.Outlined.GridView, if (grid) "Show as list" else "Show as grid") }
                        IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More") }
                        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                            if (canWrite) {
                                DropdownMenuItem(text = { Text("Upload files here") }, leadingIcon = { Icon(Icons.Outlined.Upload, null) }, onClick = { menu = false; pick.launch(arrayOf("*/*")) })
                                DropdownMenuItem(text = { Text("New folder") }, leadingIcon = { Icon(Icons.Outlined.CreateNewFolder, null) }, onClick = { menu = false; newFolder = true })
                            }
                            if (onBeside != null) DropdownMenuItem(text = { Text("Show another place beside") }, leadingIcon = { Icon(Icons.Outlined.VerticalSplit, null) }, onClick = { menu = false; onBeside() })
                            if (canShare && placeId == Place.HOME) DropdownMenuItem(text = { Text("Share link to this folder") }, leadingIcon = { Icon(Icons.Outlined.Link, null) }, onClick = { menu = false; shareFolder = true })
                            if (shown.any(pickable)) DropdownMenuItem(text = { Text("Select") }, leadingIcon = { Icon(Icons.Outlined.Checklist, null) }, onClick = { menu = false; shown.firstOrNull(pickable)?.let { vm.pick(it.path) } })
                            DropdownMenuItem(text = { Text("Refresh") }, leadingIcon = { Icon(Icons.Outlined.Refresh, null) }, onClick = { menu = false; vm.refresh() })
                        }
                    },
                )
            }
        },
        snackbarHost = { SnackbarHost(snack) },
    ) { pad ->
        Box(
            Modifier.padding(pad).fillMaxSize()
                .dragAndDropTarget(shouldStartDragAndDrop = { e -> e.mimeTypes().isNotEmpty() }, target = paneTarget)
                .let { if (dd.over == path && (path.isNotEmpty() || !isBurrow)) it.border(3.dp, MaterialTheme.colorScheme.primary) else it },
        ) {
            val r = results
            when {
                searching && r != null -> when (r) {
                    is Load.Loading -> Busy()
                    is Load.Failed -> EmptyState(Icons.Outlined.WifiOff, r.message, action = "Try again") { vm.search(query) }
                    is Load.Ready -> if (r.value.entries.isEmpty()) EmptyState(Icons.Outlined.SearchOff, "Nothing matches “$query”", if (isBurrow) "Every word has to appear somewhere in the path." else "Every word has to appear in the name.")
                    else list(r.value.entries, false, true)
                }
                searching -> if (isBurrow) EmptyState(Icons.Outlined.Search, "Search Burrow", "Type part of any file or folder name. ext:pdf and type:video narrow it down.")
                else EmptyState(Icons.Outlined.Search, "Search ${path.substringAfterLast('/').ifEmpty { place?.name ?: "here" }}", "Type part of a file or folder name.")
                else -> when (val l = listing) {
                    is Load.Loading -> Busy()
                    is Load.Failed -> if (needsAccess) EmptyState(Icons.Outlined.FolderOff, "Burrow needs access to all files", "Android asks for it once, in Settings.", action = "Allow access") { requestAccess() }
                    else EmptyState(Icons.Outlined.WifiOff, l.message, action = "Try again") { vm.refresh() }
                    is Load.Ready -> Column {
                        if (path.isNotEmpty()) Crumbs(path, dd, rootName = place?.name ?: "Burrow", rootIsFolder = !isBurrow) { vm.open(it) }
                        if (path.isEmpty() && favorites.isNotEmpty()) Favorites(favorites, open)
                        if (l.value.entries.isEmpty()) {
                            EmptyState(
                                Icons.Outlined.Folder,
                                if (path.isEmpty() && isBurrow) "No folders yet" else "This folder is empty",
                                if (path.isEmpty() && isBurrow) "Ask an admin to give you access to a folder." else if (canWrite) "Upload files or drag them here." else null,
                            )
                        } else list(l.value.entries, grid, false)
                    }
                }
            }
            dd.hint?.let { DropHint(it, Modifier.align(Alignment.BottomCenter).padding(bottom = 24.dp)) }
        }
    }
    if (adding) io.github.xattribution.burrow.ui.settings.AddConnectionDialog(onDismiss = { adding = false }, onAdded = { p -> adding = false; onPlace(p.id) })
    if (shareFolder) io.github.xattribution.burrow.ui.links.NewLinkSheet(path = path, onDismiss = { shareFolder = false }, onCreated = { vm.refresh() })
    when (val a = acting) {
        "copy", "move" -> PlaceTargetPicker(
            title = (if (a == "move") "Move " else "Copy ") + (if (pickedEntries.size == 1) pickedEntries[0].name else "${pickedEntries.size} items"),
            confirm = if (a == "move") "Move here" else "Copy here",
            onDismiss = { acting = null },
            onPick = { dst, dir ->
                acting = null
                val items = pickedEntries
                vm.picked.value = emptySet()
                val where = dir.substringAfterLast('/').ifEmpty { ctx.graph.places.get(dst)?.name ?: "Burrow" }
                val what = if (items.size == 1) items[0].name else "${items.size} items"
                if (dst == placeId && a == "move") {
                    vm.act(done = "Moved $what to $where") { fs -> items.filter { DragDrop.canMove(it.path, dir, !isBurrow) }.forEach { fs.move(it.path, if (dir.isEmpty()) it.name else "$dir/${it.name}") } }
                } else {
                    items.forEach { ctx.graph.transfers.copy(placeId, it, dst, dir, a == "move") }
                    vm.message.value = (if (a == "move") "Moving " else "Copying ") + "$what to $where"
                }
            },
        )
        "delete" -> AlertDialog(
            onDismissRequest = { acting = null },
            title = { Text("Delete " + (if (pickedEntries.size == 1) pickedEntries[0].name else "${pickedEntries.size} items") + "?") },
            text = { Text(deleteNote(place)) },
            confirmButton = {
                TextButton(onClick = {
                    acting = null
                    val items = pickedEntries
                    vm.picked.value = emptySet()
                    vm.act(done = "Deleted " + if (items.size == 1) items[0].name else "${items.size} items") { fs -> items.forEach { fs.delete(it.path) } }
                }) { Text("Delete") }
            },
            dismissButton = { TextButton(onClick = { acting = null }) { Text("Cancel") } },
        )
    }
    if (newFolder) {
        var name by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { newFolder = false },
            title = { Text("New folder") },
            text = { OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true) },
            confirmButton = { TextButton(enabled = name.isNotBlank(), onClick = { newFolder = false; vm.act { it.mkdir(if (path.isEmpty()) name.trim() else "$path/${name.trim()}") } }) { Text("Create") } },
            dismissButton = { TextButton(onClick = { newFolder = false }) { Text("Cancel") } },
        )
    }
}

/** The path as a pill: home, then each folder; the folder you're in is in the accent color. */
@Composable
private fun Crumbs(path: String, dd: DragDrop?, rootName: String, rootIsFolder: Boolean, onOpen: (String) -> Unit) {
    val segs = path.split('/')
    val scroll = rememberScrollState()
    LaunchedEffect(path) { scroll.scrollTo(scroll.maxValue) }
    Row(
        Modifier.padding(horizontal = 16.dp, vertical = 4.dp).fillMaxWidth().heightIn(min = 52.dp)
            .clip(CircleShape).background(MaterialTheme.colorScheme.surfaceContainerHigh)
            .horizontalScroll(scroll).padding(horizontal = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val homeLit = dd?.over == "" && rootIsFolder
        IconButton(
            onClick = { onOpen("") },
            modifier = Modifier
                .let { if (dd != null && rootIsFolder) it.dragAndDropTarget(shouldStartDragAndDrop = { e -> e.mimeTypes().isNotEmpty() }, target = remember(dd) { dd.target("") }) else it }
                .let { if (homeLit) it.clip(CircleShape).background(MaterialTheme.colorScheme.primary) else it },
        ) { Icon(Icons.Filled.Home, rootName, tint = if (homeLit) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurfaceVariant) }
        segs.forEachIndexed { i, s ->
            Icon(Icons.AutoMirrored.Outlined.KeyboardArrowRight, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
            val last = i == segs.size - 1
            val target = segs.take(i + 1).joinToString("/")
            val lit = dd?.over == target && !last
            Text(
                s, maxLines = 1,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = if (last) FontWeight.SemiBold else FontWeight.Normal,
                color = when { lit -> MaterialTheme.colorScheme.onPrimary; last -> MaterialTheme.colorScheme.primary; else -> MaterialTheme.colorScheme.onSurfaceVariant },
                modifier = Modifier.clip(CircleShape)
                    .let { if (last) it else it.combinedClickable(onClick = { onOpen(target) }) }
                    .let { if (dd == null || last) it else it.dragAndDropTarget(shouldStartDragAndDrop = { e -> e.mimeTypes().isNotEmpty() }, target = remember(target) { dd.target(target) }) }
                    .let { if (lit) it.background(MaterialTheme.colorScheme.primary) else it }
                    .padding(horizontal = 10.dp, vertical = 12.dp),
            )
        }
        Spacer(Modifier.width(8.dp))
    }
}

@Composable
private fun Favorites(favs: List<Entry>, onOpen: (Entry) -> Unit) {
    Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp, vertical = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        favs.forEach { f ->
            AssistChip(onClick = { onOpen(f) }, label = { Text(f.name, maxLines = 1) }, leadingIcon = { Icon(Icons.Outlined.Star, null, Modifier.size(18.dp)) })
        }
    }
}

@Composable
fun EntryList(
    entries: List<Entry>,
    grid: Boolean,
    selected: String?,
    showWhere: Boolean,
    dd: DragDrop? = null,
    place: String = Place.HOME,
    picked: Set<String> = emptySet(),
    pickedEntries: List<Entry> = emptyList(),
    onPick: ((Entry) -> Unit)? = null,
    onOpen: (Entry) -> Unit,
) {
    if (grid) {
        LazyVerticalGrid(columns = GridCells.Adaptive(112.dp), modifier = Modifier.fillMaxSize(), contentPadding = androidx.compose.foundation.layout.PaddingValues(8.dp)) {
            items(entries, key = { it.path }) { e -> Tile(e, place, e.path == selected, e.path in picked, dd?.over == e.path, Modifier.draggable(e, dd, pickedEntries), onPick?.let { { it(e) } }) { onOpen(e) } }
        }
    } else {
        LazyColumn(Modifier.fillMaxSize()) {
            items(entries, key = { it.path }) { e ->
                EntryRow(e, place, e.path == selected, e.path in picked, showWhere, dd?.over == e.path, Modifier.draggable(e, dd, pickedEntries), onPick?.let { { it(e) } }) { onOpen(e) }
                if (e != entries.last()) HorizontalDivider(Modifier.padding(start = 88.dp, end = 16.dp), color = MaterialTheme.colorScheme.outlineVariant)
            }
        }
    }
}

@Composable
private fun EntryRow(e: Entry, place: String, selected: Boolean, picked: Boolean, showWhere: Boolean, lit: Boolean, drag: Modifier, onLongClick: (() -> Unit)?, onClick: () -> Unit) {
    val ctx = LocalContext.current
    val cs = MaterialTheme.colorScheme
    ListItem(
        modifier = drag.combinedClickable(onClick = onClick, onLongClick = onLongClick, onLongClickLabel = if (onLongClick != null) "Select" else null).padding(vertical = 4.dp),
        colors = when {
            lit -> ListItemDefaults.colors(containerColor = cs.primary, headlineColor = cs.onPrimary, supportingColor = cs.onPrimary, trailingIconColor = cs.onPrimary)
            picked -> ListItemDefaults.colors(containerColor = cs.primaryContainer, headlineColor = cs.onPrimaryContainer, supportingColor = cs.onPrimaryContainer)
            else -> ListItemDefaults.colors(containerColor = if (selected) cs.surfaceContainerHigh else Color.Transparent)
        },
        leadingContent = { if (picked) PickedMark(48) else Leading(e, place, 48, if (lit) cs.onPrimary else null) },
        headlineContent = { Text(e.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        supportingContent = {
            Column {
                if (showWhere) Text(e.path.substringBeforeLast('/', "").ifEmpty { "Burrow" }, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                    Text(formatDate(e.mtime, ctx), maxLines = 1)
                    if (!e.dir) Text(formatSize(e.size), maxLines = 1)
                }
            }
        },
        trailingContent = if (e.shared > 0) ({ Icon(Icons.Outlined.Link, "Shared", tint = MaterialTheme.colorScheme.primary) }) else null,
    )
}

@Composable
private fun Tile(e: Entry, place: String, selected: Boolean, picked: Boolean, lit: Boolean, drag: Modifier, onLongClick: (() -> Unit)?, onClick: () -> Unit) {
    val cs = MaterialTheme.colorScheme
    Column(
        Modifier.padding(4.dp).clip(RoundedCornerShape(12.dp)).then(drag).combinedClickable(onClick = onClick, onLongClick = onLongClick, onLongClickLabel = if (onLongClick != null) "Select" else null)
            .background(when { lit -> cs.primary; picked -> cs.primaryContainer; selected -> cs.surfaceContainerHighest; else -> cs.surfaceContainerLow }),
    ) {
        Box(Modifier.fillMaxWidth().aspectRatio(1f), contentAlignment = Alignment.Center) {
            Leading(e, place, 0, if (lit) cs.onPrimary else null)
            if (picked) Box(Modifier.align(Alignment.TopStart).padding(8.dp)) { PickedMark(28) }
        }
        Text(e.name, style = MaterialTheme.typography.labelMedium, color = when { lit -> cs.onPrimary; picked -> cs.onPrimaryContainer; else -> Color.Unspecified }, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(8.dp))
    }
}

/** A thumbnail for pictures, an icon for everything else. size 0 fills the space. */
@Composable
private fun Leading(e: Entry, place: String, size: Int, tint: Color? = null) {
    val m = if (size > 0) Modifier.size(size.dp).clip(RoundedCornerShape(8.dp)) else Modifier.fillMaxSize()
    // pictures everywhere; videos on the phone itself, where a frame is cheap to read
    if (!e.dir && (e.kind == "image" || (e.kind == "video" && place.startsWithPhone()))) {
        AsyncImage(model = ThumbFetcher.Thumb(e.path, e.mtime, place), contentDescription = null, contentScale = ContentScale.Crop, modifier = m.background(MaterialTheme.colorScheme.surfaceVariant))
    } else {
        Box(m, contentAlignment = Alignment.Center) {
            Icon(if (e.dir) Icons.Filled.Folder else kindIcon(e.kind, false), null, Modifier.size(if (size > 0) 36.dp else 48.dp), tint = tint ?: if (e.dir) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

private fun String.startsWithPhone() = this == Place.PHONE || startsWith("vol-")

/** A picked item: a check in a solid circle where its icon was. */
@Composable
private fun PickedMark(size: Int) {
    Box(Modifier.size(size.dp).clip(CircleShape).background(MaterialTheme.colorScheme.primary), contentAlignment = Alignment.Center) {
        Icon(Icons.Filled.Check, "Selected", Modifier.size((size * 0.6f).dp), tint = MaterialTheme.colorScheme.onPrimary)
    }
}

/** Hold and move to drag (a picked item takes the others along); folders also take drops. Dims while it's the thing being dragged. */
@Composable
private fun Modifier.draggable(e: Entry, dd: DragDrop?, picked: List<Entry> = emptyList()): Modifier {
    if (dd == null || isLibraryRoot(e, dd)) return this
    return this
        .let { if (dd.lifted == e.path) it.alpha(0.45f) else it }
        .dragAndDropSource { _ -> dd.transfer(e, picked) }
        .let { if (e.dir) it.dragAndDropTarget(shouldStartDragAndDrop = { ev -> ev.mimeTypes().isNotEmpty() }, target = remember(e.path) { dd.target(e.path) }) else it }
}

/** Where a drop goes, as a solid pill like the system's drag badge. */
@Composable
private fun DropHint(text: String, modifier: Modifier) {
    Text(
        text, maxLines = 1, overflow = TextOverflow.Ellipsis,
        style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold,
        color = MaterialTheme.colorScheme.onPrimary,
        modifier = modifier.clip(CircleShape).background(MaterialTheme.colorScheme.primary).padding(horizontal = 20.dp, vertical = 12.dp),
    )
}

/** A Burrow's top entries are libraries, which don't move; an SMB share's are ordinary files. */
private fun isLibraryRoot(e: Entry, dd: DragDrop) = !e.path.contains('/') && dd.isBurrow

/** The place's name with a menu to switch places or add one. */
@Composable
private fun PlaceSwitcher(places: List<Place>, current: String, onPlace: (String) -> Unit, onAdd: () -> Unit) {
    var open by remember { mutableStateOf(false) }
    val name = places.firstOrNull { it.id == current }?.name ?: "Burrow"
    Box {
        Row(
            Modifier.clip(RoundedCornerShape(12.dp)).combinedClickable(onClick = { open = true }).padding(end = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(name, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Icon(Icons.Filled.ArrowDropDown, "Switch place")
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            places.forEach { p ->
                DropdownMenuItem(
                    text = { Text(p.name, fontWeight = if (p.id == current) FontWeight.SemiBold else FontWeight.Normal) },
                    leadingIcon = { Icon(placeIcon(p), null, tint = if (p.id == current) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant) },
                    trailingIcon = if (p.id == current) ({ Icon(Icons.Filled.Check, null, tint = MaterialTheme.colorScheme.primary) }) else null,
                    onClick = { open = false; if (p.id != current) onPlace(p.id) },
                )
            }
            HorizontalDivider()
            DropdownMenuItem(text = { Text("Add a connection") }, leadingIcon = { Icon(Icons.Outlined.Add, null) }, onClick = { open = false; onAdd() })
        }
    }
}

fun placeIcon(p: Place) = when (p) {
    is Place.Smb -> Icons.Outlined.Lan
    is Place.Peer -> Icons.Outlined.Dns
    is Place.Phone -> if (!p.removable) Icons.Outlined.PhoneAndroid else if (p.name.contains("usb", ignoreCase = true)) Icons.Outlined.Usb else Icons.Outlined.SdCard
    is Place.Folder -> Icons.Outlined.FolderShared
    else -> Icons.Outlined.Storage
}
