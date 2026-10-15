@file:OptIn(ExperimentalMaterial3Api::class)

package io.github.xattribution.burrow.ui.files

import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.DriveFileMove
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.FileCopy
import androidx.compose.material.icons.outlined.SelectAll
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.text.style.TextOverflow
import io.github.xattribution.burrow.core.Entry
import io.github.xattribution.burrow.data.Place
import io.github.xattribution.burrow.data.RemoteFileProvider

/** The bar while items are picked: how many, and what to do with them all. */
@Composable
fun PickBar(
    count: Int,
    canChange: Boolean,
    hasFiles: Boolean,
    onClear: () -> Unit,
    onAll: () -> Unit,
    onSend: () -> Unit,
    onCopy: () -> Unit,
    onMove: () -> Unit,
    onDelete: () -> Unit,
) {
    TopAppBar(
        colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh),
        navigationIcon = { IconButton(onClick = onClear) { Icon(Icons.Outlined.Close, "Stop selecting") } },
        title = { Text("$count selected", style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        actions = {
            if (hasFiles) IconButton(onClick = onSend) { Icon(Icons.Outlined.Share, "Send") }
            IconButton(onClick = onCopy) { Icon(Icons.Outlined.FileCopy, "Copy to") }
            if (canChange) IconButton(onClick = onMove) { Icon(Icons.AutoMirrored.Outlined.DriveFileMove, "Move to") }
            var more by remember { mutableStateOf(false) }
            IconButton(onClick = { more = true }) { Icon(Icons.Outlined.MoreVert, "More") }
            DropdownMenu(expanded = more, onDismissRequest = { more = false }) {
                DropdownMenuItem(text = { Text("Select all") }, leadingIcon = { Icon(Icons.Outlined.SelectAll, null) }, onClick = { more = false; onAll() })
                if (canChange) DropdownMenuItem(text = { Text("Delete") }, leadingIcon = { Icon(Icons.Outlined.Delete, null) }, onClick = { more = false; onDelete() })
            }
        },
    )
}

/**
 * Hands files to another app through the share sheet (a message, an email, a
 * chat). The app reads each one straight from where it is, through
 * [RemoteFileProvider], so nothing downloads first.
 */
fun sendFiles(ctx: Context, place: String, files: List<Entry>) {
    if (files.isEmpty()) return
    val uris: List<Uri> = files.map { RemoteFileProvider.uriFor(ctx, it, place) }
    val types = files.map { RemoteFileProvider.mimeOf(it.name) }.distinct()
    val type = types.singleOrNull() ?: types.map { it.substringBefore('/') }.distinct().singleOrNull()?.let { "$it/*" } ?: "*/*"
    val i = if (uris.size == 1) {
        Intent(Intent.ACTION_SEND).putExtra(Intent.EXTRA_STREAM, uris[0])
    } else {
        Intent(Intent.ACTION_SEND_MULTIPLE).putParcelableArrayListExtra(Intent.EXTRA_STREAM, ArrayList(uris))
    }
    i.type = type
    // the clip carries the read grant to whichever app is picked
    i.clipData = ClipData(files[0].name, types.toTypedArray(), ClipData.Item(uris[0])).apply { uris.drop(1).forEach { addItem(ClipData.Item(it)) } }
    i.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    try {
        ctx.startActivity(Intent.createChooser(i, if (files.size == 1) "Send ${files[0].name}" else "Send ${files.size} files").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    } catch (e: ActivityNotFoundException) {
        throw IllegalStateException("No app on this phone takes files.")
    }
}

/** What deleting does in [place], for one item or several. */
fun deleteNote(place: Place?, many: Boolean = true): String {
    val subj = if (many) "They" else "It"
    val are = if (many) "they're" else "it's"
    return when (place) {
        is Place.Home, is Place.Peer, null -> "$subj go${if (many) "" else "es"} to Burrow's trash and can be restored for 30 days."
        is Place.Phone -> "$subj${if (many) "'re" else "'s"} deleted from " + (if (place.removable) "the ${place.name}" else "this phone") + " for good."
        is Place.Smb -> "This share has no trash, so $are gone for good."
        is Place.Folder -> "The app that holds ${if (many) "them" else "it"} decides; most delete for good."
    }
}
