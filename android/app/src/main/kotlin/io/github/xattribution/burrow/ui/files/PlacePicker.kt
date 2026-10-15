@file:OptIn(ExperimentalMaterial3Api::class)

package io.github.xattribution.burrow.ui.files

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.WifiOff
import androidx.compose.material3.BottomAppBar
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import io.github.xattribution.burrow.core.Listing
import io.github.xattribution.burrow.data.Place
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.ui.common.Busy
import io.github.xattribution.burrow.ui.common.EmptyState
import io.github.xattribution.burrow.ui.common.Load
import io.github.xattribution.burrow.ui.common.friendly

/** Pick a folder in any place (or only the places [only] allows), to copy or move something there. */
@Composable
fun PlaceTargetPicker(
    title: String,
    confirm: String,
    onDismiss: () -> Unit,
    start: String = Place.HOME,
    only: (Place) -> Boolean = { true },
    onPick: (place: String, dir: String) -> Unit,
) {
    val g = LocalContext.current.graph
    val places = g.places.list.collectAsState().value.filter(only)
    var place by remember { mutableStateOf(start) }
    var path by remember { mutableStateOf("") }
    var state by remember { mutableStateOf<Load<Listing>>(Load.Loading) }
    var reload by remember { mutableStateOf(0) }
    var menu by remember { mutableStateOf(false) }
    val fs = remember(place) { g.places.fs(place) }
    LaunchedEffect(place, path, reload) {
        state = Load.Loading
        state = try {
            Load.Ready(fs.ls(path))
        } catch (e: Exception) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            Load.Failed(friendly(e))
        }
    }
    val listing = (state as? Load.Ready)?.value
    val usable = (path.isNotEmpty() || !fs.isBurrow) && listing?.canWrite == true
    val placeName = places.firstOrNull { it.id == place }?.name ?: "Burrow"
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        BackHandler(enabled = path.isNotEmpty()) { path = path.substringBeforeLast('/', "") }
        Scaffold(
            topBar = {
                TopAppBar(
                    navigationIcon = {
                        if (path.isEmpty()) IconButton(onClick = onDismiss) { Icon(Icons.Outlined.Close, "Cancel") }
                        else IconButton(onClick = { path = path.substringBeforeLast('/', "") }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Up") }
                    },
                    title = {
                        Column {
                            Text(title, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Box {
                                Row(Modifier.clickable { menu = true }, verticalAlignment = Alignment.CenterVertically) {
                                    Text(placeName + if (path.isNotEmpty()) " / $path" else "", style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    Icon(Icons.Filled.ArrowDropDown, "Switch place")
                                }
                                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                                    places.forEach { p ->
                                        DropdownMenuItem(text = { Text(p.name) }, leadingIcon = { Icon(placeIcon(p), null) }, onClick = { menu = false; place = p.id; path = "" })
                                    }
                                }
                            }
                        }
                    },
                )
            },
            bottomBar = {
                BottomAppBar {
                    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text(if (usable) "Into ${path.substringAfterLast('/').ifEmpty { placeName }}" else "Open a folder you can add to", style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f), maxLines = 2)
                        Button(enabled = usable, onClick = { onPick(place, path) }) { Text(confirm) }
                    }
                }
            },
        ) { pad ->
            Box(Modifier.padding(pad).fillMaxSize()) {
                when (val s = state) {
                    is Load.Loading -> Busy()
                    is Load.Failed -> EmptyState(Icons.Outlined.WifiOff, s.message, action = "Try again") { reload++ }
                    is Load.Ready -> {
                        val dirs = s.value.entries.filter { it.dir }
                        if (dirs.isEmpty()) EmptyState(Icons.Outlined.Folder, "No folders inside", null)
                        else LazyColumn {
                            items(dirs, key = { it.path }) { d ->
                                ListItem(
                                    modifier = Modifier.clickable { path = d.path },
                                    leadingContent = { Icon(Icons.Outlined.Folder, null, tint = MaterialTheme.colorScheme.primary) },
                                    headlineContent = { Text(d.name) },
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}
