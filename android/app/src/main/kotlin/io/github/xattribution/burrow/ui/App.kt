package io.github.xattribution.burrow.ui

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.outlined.Sync
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffold
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.ui.files.FilesScreen
import io.github.xattribution.burrow.ui.links.LinksScreen
import io.github.xattribution.burrow.ui.pair.PairScreen
import io.github.xattribution.burrow.ui.settings.SettingsScreen
import io.github.xattribution.burrow.ui.sync.SyncScreen

enum class Dest(val label: String, val icon: ImageVector) {
    FILES("Files", Icons.Outlined.Folder),
    SYNC("Sync", Icons.Outlined.Sync),
    LINKS("Links", Icons.Outlined.Link),
    SETTINGS("Settings", Icons.Outlined.Settings),
}

/** Not paired: the pairing screen. Paired: the app, with navigation that fits the window. */
@Composable
fun BurrowUi(pairLink: String?, onLinkUsed: () -> Unit) {
    val g = LocalContext.current.graph
    val paired by g.device.state.collectAsState()
    if (paired == null) {
        PairScreen(initialLink = pairLink, onLinkUsed = onLinkUsed)
        return
    }
    var dest by rememberSaveable { mutableStateOf(Dest.FILES) }
    // bottom bar on a phone or folded screen, a rail when unfolded or on a tablet
    // the bar sits on the same black as the screen; only the selected item is marked
    val black = MaterialTheme.colorScheme.background
    NavigationSuiteScaffold(
        containerColor = black,
        navigationSuiteColors = NavigationSuiteDefaults.colors(navigationBarContainerColor = black, navigationRailContainerColor = black),
        navigationSuiteItems = {
            Dest.entries.forEach { d ->
                item(selected = d == dest, onClick = { dest = d }, icon = { Icon(d.icon, contentDescription = null) }, label = { Text(d.label) })
            }
        },
    ) {
        when (dest) {
            Dest.FILES -> FilesScreen()
            Dest.SYNC -> SyncScreen()
            Dest.LINKS -> LinksScreen()
            Dest.SETTINGS -> SettingsScreen(pairLink = pairLink, onLinkUsed = onLinkUsed)
        }
    }
}
