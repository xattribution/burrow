package io.github.xattribution.burrow.ui.settings

import android.Manifest
import android.content.ClipboardManager
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import io.github.xattribution.burrow.core.PairingLink
import io.github.xattribution.burrow.data.Place
import io.github.xattribution.burrow.data.SmbSettings
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.ui.common.friendly
import io.github.xattribution.burrow.ui.files.placeIcon
import io.github.xattribution.burrow.ui.pair.QrScanner
import kotlinx.coroutines.launch

/**
 * The places this phone connects to besides its own Burrow. They stay on the
 * phone; "Link to my NAS" hands one to your Burrow when you want it there too.
 */
@Composable
fun ConnectionRow(p: Place, homeIsAdmin: Boolean, onMessage: (String) -> Unit) {
    val g = LocalContext.current.graph
    val scope = rememberCoroutineScope()
    var menu by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf(false) }
    val where = when (p) {
        is Place.Smb -> "SMB ${p.settings.host}/${p.settings.share}" + if (p.settings.folder.isNotEmpty()) "/${p.settings.folder}" else ""
        is Place.Peer -> p.store.current?.info?.let { i -> "Burrow ${i.public.ifBlank { i.lan }}, as ${i.user}" } ?: "Burrow"
        is Place.Folder -> "App folder, ${p.tree.authority.orEmpty()}"
        else -> ""
    }
    ListItem(
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
        leadingContent = { Icon(placeIcon(p), null, tint = MaterialTheme.colorScheme.primary) },
        headlineContent = { Text(p.name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        supportingContent = { Text(where, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        trailingContent = {
            IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More for ${p.name}") }
            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                if (homeIsAdmin && (p is Place.Smb || p is Place.Peer)) DropdownMenuItem(text = { Text("Link to my NAS") }, onClick = {
                    menu = false
                    scope.launch { onMessage(runCatching { g.places.linkToHome(p.id, homeIsAdmin) }.getOrElse { friendly(it) }) }
                })
                DropdownMenuItem(text = { Text("Remove") }, onClick = { menu = false; removing = true })
            }
        },
    )
    if (removing) AlertDialog(
        onDismissRequest = { removing = false },
        title = { Text("Remove ${p.name}?") },
        text = {
            Text(
                when (p) {
                    is Place.Peer -> "This phone is removed from that Burrow too. Files there stay where they are."
                    is Place.Folder -> "Burrow lets go of the folder. Its files stay where they are."
                    else -> "The sign-in is forgotten. Files on the share stay where they are."
                },
            )
        },
        confirmButton = { TextButton(onClick = { removing = false; scope.launch { g.places.remove(p.id) } }) { Text("Remove") } },
        dismissButton = { TextButton(onClick = { removing = false }) { Text("Cancel") } },
    )
}

/** Add another Burrow (pair with its code) or an SMB share. */
@Composable
fun AddConnectionDialog(onDismiss: () -> Unit, onAdded: (Place) -> Unit) {
    val ctx = LocalContext.current
    val g = ctx.graph
    val scope = rememberCoroutineScope()
    var kind by remember { mutableStateOf("burrow") }
    var name by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    // another Burrow
    var link by remember { mutableStateOf("") }
    var scanning by remember { mutableStateOf(false) }
    val camera = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok -> if (ok) scanning = true else error = "Scanning needs the camera. Paste the link instead." }
    // a folder from another app (a cloud drive app, Termux, a USB drive the system shows)
    val folderPicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
        if (uri != null) {
            try {
                ctx.contentResolver.takePersistableUriPermission(uri, android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION or android.content.Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
                onAdded(g.places.addFolder(uri))
            } catch (e: Exception) {
                error = friendly(e)
            }
        }
    }
    // SMB
    var host by remember { mutableStateOf("") }
    var port by remember { mutableStateOf("") }
    var share by remember { mutableStateOf("") }
    var user by remember { mutableStateOf("") }
    var pass by remember { mutableStateOf("") }
    var domain by remember { mutableStateOf("") }
    var folder by remember { mutableStateOf("") }

    val parsed = remember(link) { link.trim().takeIf { it.isNotEmpty() }?.let { runCatching { PairingLink.parse(it) }.getOrNull() } }
    val ready = !busy && when (kind) {
        "burrow" -> parsed != null
        "folder" -> false
        else -> host.isNotBlank() && share.isNotBlank()
    }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text("Add a connection") },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                    listOf("burrow" to "Burrow", "smb" to "SMB share", "folder" to "App folder").forEachIndexed { i, (k, l) ->
                        SegmentedButton(selected = kind == k, onClick = { kind = k; error = null }, shape = SegmentedButtonDefaults.itemShape(i, 3)) { Text(l, maxLines = 1) }
                    }
                }
                if (kind != "folder") OutlinedTextField(name, { name = it }, label = { Text("Name") }, placeholder = { Text(if (kind == "smb") "Office NAS" else "Alex's Burrow") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                if (kind == "folder") {
                    OutlinedButton(onClick = { folderPicker.launch(null) }) { Text("Pick a folder from an app") }
                } else if (kind == "burrow") {
                    if (scanning) {
                        QrScanner(onCode = { link = it; scanning = false }, modifier = Modifier.fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(16.dp)))
                        TextButton(onClick = { scanning = false }) { Text("Cancel scanning") }
                    } else {
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            OutlinedButton(onClick = {
                                if (ContextCompat.checkSelfPermission(ctx, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) scanning = true
                                else camera.launch(Manifest.permission.CAMERA)
                            }) { Text("Scan the code") }
                            OutlinedButton(onClick = {
                                val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                                cm.primaryClip?.getItemAt(0)?.text?.toString()?.let { link = it }
                            }) { Text("Paste link") }
                        }
                        OutlinedTextField(link, { link = it }, label = { Text("Pairing link") }, isError = link.isNotBlank() && parsed == null, singleLine = true, modifier = Modifier.fillMaxWidth())
                        parsed?.let { l -> Text("Reaches it at ${listOfNotNull(l.lan, l.public).joinToString(" or ")}", style = MaterialTheme.typography.bodySmall) }
                    }
                } else {
                    OutlinedTextField(host, { host = it.trim() }, label = { Text("Address") }, placeholder = { Text("192.168.1.20") }, singleLine = true, modifier = Modifier.fillMaxWidth(), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri))
                    OutlinedTextField(share, { share = it.trim() }, label = { Text("Share") }, placeholder = { Text("Media") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                    OutlinedTextField(user, { user = it }, label = { Text("User name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                    OutlinedTextField(pass, { pass = it }, label = { Text("Password") }, singleLine = true, visualTransformation = PasswordVisualTransformation(), modifier = Modifier.fillMaxWidth(), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password))
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedTextField(port, { port = it.filter(Char::isDigit) }, label = { Text("Port") }, placeholder = { Text("445") }, singleLine = true, modifier = Modifier.weight(1f), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number))
                        OutlinedTextField(domain, { domain = it }, label = { Text("Domain") }, singleLine = true, modifier = Modifier.weight(1f))
                    }
                    OutlinedTextField(folder, { folder = it.trim() }, label = { Text("Start in folder") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                }
                error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            }
        },
        confirmButton = {
            TextButton(enabled = ready, onClick = {
                busy = true
                error = null
                scope.launch {
                    try {
                        val p = if (kind == "burrow") g.places.pairPeer(parsed!!, name.trim(), g.device.current?.info?.deviceName?.ifBlank { null } ?: Build.MODEL)
                        else g.places.addSmb(name.trim(), SmbSettings(host = host, port = port.toIntOrNull() ?: 445, share = share.trim('/', '\\'), user = user, password = pass, domain = domain, folder = folder.trim('/', '\\')))
                        onAdded(p)
                    } catch (e: Exception) {
                        error = friendly(e)
                    } finally {
                        busy = false
                    }
                }
            }) { Text(if (busy) "Connecting…" else if (kind == "burrow") "Pair" else "Connect") }
        },
        dismissButton = { TextButton(enabled = !busy, onClick = onDismiss) { Text("Cancel") } },
    )
}
