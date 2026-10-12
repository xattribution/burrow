@file:OptIn(ExperimentalMaterial3Api::class, ExperimentalMaterial3AdaptiveApi::class, ExperimentalFoundationApi::class)

package io.github.xattribution.burrow.ui.files

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.view.DragEvent
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.draganddrop.dragAndDropTarget
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
import androidx.compose.ui.draganddrop.DragAndDropEvent
import androidx.compose.ui.draganddrop.DragAndDropTarget
import androidx.compose.ui.draganddrop.mimeTypes
import androidx.compose.ui.draganddrop.toAndroidDragEvent
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import coil3.compose.AsyncImage
import io.github.xattribution.burrow.core.Entry
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.media.ThumbFetcher
import io.github.xattribution.burrow.ui.common.Busy
import io.github.xattribution.burrow.ui.common.EmptyState
import io.github.xattribution.burrow.ui.common.Load
import io.github.xattribution.burrow.ui.common.formatSize
import io.github.xattribution.burrow.ui.common.formatWhen
import io.github.xattribution.burrow.ui.common.kindIcon
import io.github.xattribution.burrow.work.stageForUpload
import kotlinx.coroutines.launch

/**
 * Browse and search Burrow. On a phone (or a folded foldable) the folder and a
 * file's details take turns on screen; with room for two panes (unfolded, a
 * tablet, landscape) they sit side by side, clear of the hinge.
 */
@Composable
fun FilesScreen() {
    val conn = LocalContext.current.graph.connection
    val vm: FilesViewModel = viewModel { FilesViewModel(conn) }
    val navigator = rememberListDetailPaneScaffoldNavigator<String>()
    val scope = rememberCoroutineScope()
    val twoPane = navigator.scaffoldValue[ListDetailPaneScaffoldRole.List] == PaneAdaptedValue.Expanded &&
        navigator.scaffoldValue[ListDetailPaneScaffoldRole.Detail] == PaneAdaptedValue.Expanded
    val selected = navigator.currentDestination?.contentKey
    NavigableListDetailPaneScaffold(
        navigator = navigator,
        listPane = {
            AnimatedPane {
                FolderPane(vm, selected = if (twoPane) selected else null) { e ->
                    if (e.dir) vm.open(e.path) else scope.launch { navigator.navigateTo(ListDetailPaneScaffoldRole.Detail, e.path) }
                }
            }
        },
        detailPane = {
            AnimatedPane {
                val p = navigator.currentDestination?.contentKey
                if (p == null) {
                    EmptyState(Icons.Outlined.Folder, "Nothing selected", "Choose a file to see it here.")
                } else {
                    DetailPane(
                        path = p,
                        showBack = !twoPane,
                        onBack = { scope.launch { navigator.navigateBack() } },
                        onChanged = { vm.refresh() },
                        onOpenFolder = { folder -> vm.open(folder); scope.launch { navigator.navigateBack() } },
                        onRenamed = { np -> vm.refresh(); scope.launch { navigator.navigateTo(ListDetailPaneScaffoldRole.Detail, np) } },
                        onDeleted = { vm.refresh(); scope.launch { navigator.navigateBack() } },
                    )
                }
            }
        },
    )
}

@Composable
private fun FolderPane(vm: FilesViewModel, selected: String?, onOpen: (Entry) -> Unit) {
    val ctx = LocalContext.current
    val path by vm.path.collectAsState()
    val listing by vm.listing.collectAsState()
    val query by vm.query.collectAsState()
    val results by vm.results.collectAsState()
    val favorites by vm.favorites.collectAsState()
    val grid by vm.grid.collectAsState()
    val message by vm.message.collectAsState()
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
    BackHandler(enabled = searching || path.isNotEmpty()) {
        if (searching) { searching = false; vm.search("") } else vm.up()
    }
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris: List<Uri> ->
        for (u in uris) {
            runCatching { ctx.contentResolver.takePersistableUriPermission(u, Intent.FLAG_GRANT_READ_URI_PERMISSION) }
            ctx.graph.transfers.upload(u, path)
        }
        if (uris.isNotEmpty()) vm.message.value = "Sending ${uris.size} file${if (uris.size == 1) "" else "s"} to ${path.substringAfterLast('/')}"
    }

    // files dragged in from another app (split screen, a foldable's other half) are uploaded here
    val dropTarget = remember(path, canWrite) {
        object : DragAndDropTarget {
            override fun onDrop(event: DragAndDropEvent): Boolean {
                val de: DragEvent = event.toAndroidDragEvent()
                (ctx as? Activity)?.requestDragAndDropPermissions(de)
                val clip = de.clipData ?: return false
                var n = 0
                for (i in 0 until clip.itemCount) {
                    val u = clip.getItemAt(i).uri ?: continue
                    ctx.graph.transfers.upload(stageForUpload(ctx, u), path)
                    n++
                }
                if (n > 0) vm.message.value = "Sending $n file${if (n == 1) "" else "s"} here"
                return n > 0
            }
        }
    }

    Scaffold(
        topBar = {
            if (searching) {
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
                TopAppBar(
                    navigationIcon = { if (path.isNotEmpty()) IconButton(onClick = { vm.up() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Up") } },
                    title = { Text(path.substringAfterLast('/').ifEmpty { "Burrow" }, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                    actions = {
                        IconButton(onClick = { searching = true }) { Icon(Icons.Outlined.Search, "Search") }
                        IconButton(onClick = { vm.grid.value = !grid }) { Icon(if (grid) Icons.Outlined.ViewList else Icons.Outlined.GridView, if (grid) "Show as list" else "Show as grid") }
                        IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More") }
                        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                            if (canWrite) {
                                DropdownMenuItem(text = { Text("Upload files here") }, leadingIcon = { Icon(Icons.Outlined.Upload, null) }, onClick = { menu = false; pick.launch(arrayOf("*/*")) })
                                DropdownMenuItem(text = { Text("New folder") }, leadingIcon = { Icon(Icons.Outlined.CreateNewFolder, null) }, onClick = { menu = false; newFolder = true })
                            }
                            if (canShare) DropdownMenuItem(text = { Text("Share link to this folder") }, leadingIcon = { Icon(Icons.Outlined.Link, null) }, onClick = { menu = false; shareFolder = true })
                            DropdownMenuItem(text = { Text("Refresh") }, leadingIcon = { Icon(Icons.Outlined.Refresh, null) }, onClick = { menu = false; vm.refresh() })
                        }
                    },
                )
            }
        },
        snackbarHost = { SnackbarHost(snack) },
    ) { pad ->
        Box(
            Modifier.padding(pad).fillMaxSize().let { if (canWrite) it.dragAndDropTarget(shouldStartDragAndDrop = { e -> e.mimeTypes().isNotEmpty() }, target = dropTarget) else it },
        ) {
            val r = results
            when {
                searching && r != null -> when (r) {
                    is Load.Loading -> Busy()
                    is Load.Failed -> EmptyState(Icons.Outlined.WifiOff, r.message, action = "Try again") { vm.search(query) }
                    is Load.Ready -> if (r.value.entries.isEmpty()) EmptyState(Icons.Outlined.SearchOff, "Nothing matches “$query”", "Every word has to appear somewhere in the path.")
                    else EntryList(r.value.entries, grid = false, selected = selected, showWhere = true, onOpen = open)
                }
                searching -> EmptyState(Icons.Outlined.Search, "Search Burrow", "Type part of any file or folder name. ext:pdf and type:video narrow it down.")
                else -> when (val l = listing) {
                    is Load.Loading -> Busy()
                    is Load.Failed -> EmptyState(Icons.Outlined.WifiOff, l.message, action = "Try again") { vm.refresh() }
                    is Load.Ready -> Column {
                        if (path.isNotEmpty()) Crumbs(path) { vm.open(it) }
                        if (path.isEmpty() && favorites.isNotEmpty()) Favorites(favorites, open)
                        if (l.value.entries.isEmpty()) {
                            EmptyState(Icons.Outlined.Folder, if (path.isEmpty()) "No folders yet" else "This folder is empty", if (path.isEmpty()) "Ask an admin to give you access to a folder." else if (canWrite) "Upload files or drag them here." else null)
                        } else EntryList(l.value.entries, grid = grid, selected = selected, showWhere = false, onOpen = open)
                    }
                }
            }
        }
    }
    if (shareFolder) io.github.xattribution.burrow.ui.links.NewLinkSheet(path = path, onDismiss = { shareFolder = false }, onCreated = { vm.refresh() })
    if (newFolder) {
        var name by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { newFolder = false },
            title = { Text("New folder") },
            text = { OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true) },
            confirmButton = { TextButton(enabled = name.isNotBlank(), onClick = { newFolder = false; vm.act { it.mkdir("$path/${name.trim()}") } }) { Text("Create") } },
            dismissButton = { TextButton(onClick = { newFolder = false }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun Crumbs(path: String, onOpen: (String) -> Unit) {
    val segs = path.split('/')
    Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically) {
        TextButton(onClick = { onOpen("") }) { Text("Burrow") }
        segs.forEachIndexed { i, s ->
            Text("/", color = MaterialTheme.colorScheme.onSurfaceVariant)
            val target = segs.take(i + 1).joinToString("/")
            TextButton(onClick = { onOpen(target) }, enabled = i < segs.size - 1) { Text(s, maxLines = 1) }
        }
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
fun EntryList(entries: List<Entry>, grid: Boolean, selected: String?, showWhere: Boolean, onOpen: (Entry) -> Unit) {
    if (grid) {
        LazyVerticalGrid(columns = GridCells.Adaptive(112.dp), modifier = Modifier.fillMaxSize(), contentPadding = androidx.compose.foundation.layout.PaddingValues(8.dp)) {
            items(entries, key = { it.path }) { e -> Tile(e, e.path == selected) { onOpen(e) } }
        }
    } else {
        LazyColumn(Modifier.fillMaxSize()) {
            items(entries, key = { it.path }) { e -> EntryRow(e, e.path == selected, showWhere) { onOpen(e) } }
        }
    }
}

@Composable
private fun EntryRow(e: Entry, selected: Boolean, showWhere: Boolean, onClick: () -> Unit) {
    ListItem(
        modifier = Modifier.combinedClickable(onClick = onClick),
        colors = if (selected) ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.secondaryContainer) else ListItemDefaults.colors(),
        leadingContent = { Leading(e, 40) },
        headlineContent = { Text(e.name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        supportingContent = {
            val bits = buildList {
                if (showWhere) add(e.path.substringBeforeLast('/', ""))
                if (!e.dir) add(formatSize(e.size))
                add(formatWhen(e.mtime))
            }
            Text(bits.filter { it.isNotEmpty() }.joinToString(" · "), maxLines = 1, overflow = TextOverflow.Ellipsis)
        },
        trailingContent = if (e.shared > 0) ({ Icon(Icons.Outlined.Link, "Shared", tint = MaterialTheme.colorScheme.primary) }) else null,
    )
}

@Composable
private fun Tile(e: Entry, selected: Boolean, onClick: () -> Unit) {
    Column(
        Modifier.padding(4.dp).clip(RoundedCornerShape(12.dp)).combinedClickable(onClick = onClick)
            .background(if (selected) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.4f)),
    ) {
        Box(Modifier.fillMaxWidth().aspectRatio(1f), contentAlignment = Alignment.Center) { Leading(e, 0) }
        Text(e.name, style = MaterialTheme.typography.labelMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(8.dp))
    }
}

/** A thumbnail for pictures, an icon for everything else. size 0 fills the space. */
@Composable
private fun Leading(e: Entry, size: Int) {
    val m = if (size > 0) Modifier.size(size.dp).clip(RoundedCornerShape(8.dp)) else Modifier.fillMaxSize()
    if (!e.dir && e.kind == "image") {
        AsyncImage(model = ThumbFetcher.Thumb(e.path, e.mtime), contentDescription = null, contentScale = ContentScale.Crop, modifier = m.background(MaterialTheme.colorScheme.surfaceVariant))
    } else {
        Box(m, contentAlignment = Alignment.Center) {
            Icon(kindIcon(e.kind, e.dir), null, Modifier.size(if (size > 0) 28.dp else 40.dp), tint = if (e.dir) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
