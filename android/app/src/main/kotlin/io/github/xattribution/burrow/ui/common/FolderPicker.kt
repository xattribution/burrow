@file:OptIn(ExperimentalMaterial3Api::class)

package io.github.xattribution.burrow.ui.common

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
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.CreateNewFolder
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.WifiOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.BottomAppBar
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
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
import io.github.xattribution.burrow.core.Listing
import io.github.xattribution.burrow.graph
import kotlinx.coroutines.launch

/**
 * Pick a folder on Burrow. With [needWrite], only folders that take uploads can
 * be chosen (an edit grant and a writable mount); new folders can be made there.
 */
@Composable
fun BurrowFolderPicker(title: String, needWrite: Boolean, confirm: String, onPick: (String) -> Unit, onCancel: () -> Unit, start: String = "") {
    val conn = LocalContext.current.graph.connection
    val scope = rememberCoroutineScope()
    var path by rememberSaveable { mutableStateOf(start) }
    var state by remember { mutableStateOf<Load<Listing>>(Load.Loading) }
    var reload by remember { mutableIntStateOf(0) }
    var making by remember { mutableStateOf(false) }
    LaunchedEffect(path, reload) {
        state = Load.Loading
        state = try {
            Load.Ready(conn.use { it.ls(path) })
        } catch (e: Exception) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            Load.Failed(friendly(e))
        }
    }
    BackHandler(enabled = path.isNotEmpty()) { path = path.substringBeforeLast('/', "") }
    val listing = (state as? Load.Ready)?.value
    val usable = path.isNotEmpty() && (!needWrite || listing?.canWrite == true)
    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    if (path.isEmpty()) IconButton(onClick = onCancel) { Icon(Icons.Outlined.Close, "Cancel") }
                    else IconButton(onClick = { path = path.substringBeforeLast('/', "") }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Up") }
                },
                title = {
                    Column {
                        Text(title, style = MaterialTheme.typography.titleMedium)
                        Text(path.ifEmpty { "Burrow" }, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                },
                actions = { if (needWrite && listing?.canWrite == true) IconButton(onClick = { making = true }) { Icon(Icons.Outlined.CreateNewFolder, "New folder") } },
            )
        },
        bottomBar = {
            BottomAppBar {
                Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        when {
                            path.isEmpty() -> "Open a folder"
                            usable -> "Here: $path"
                            else -> "You can't add files here"
                        },
                        style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f), maxLines = 2,
                    )
                    Button(enabled = usable, onClick = { onPick(path) }) { Text(confirm) }
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
                    if (dirs.isEmpty()) EmptyState(Icons.Outlined.Folder, "No folders inside", if (usable) "Choose this one, or make a new folder." else null)
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
    if (making) {
        var name by remember { mutableStateOf("") }
        var err by remember { mutableStateOf<String?>(null) }
        AlertDialog(
            onDismissRequest = { making = false },
            title = { Text("New folder") },
            text = { OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true, isError = err != null, supportingText = err?.let { { Text(it) } }) },
            confirmButton = {
                TextButton(enabled = name.isNotBlank(), onClick = {
                    scope.launch {
                        try {
                            val e = conn.use { it.mkdir("$path/${name.trim()}") }
                            making = false
                            path = e.path
                        } catch (x: Exception) {
                            err = friendly(x)
                        }
                    }
                }) { Text("Create") }
            },
            dismissButton = { TextButton(onClick = { making = false }) { Text("Cancel") } },
        )
    }
}
