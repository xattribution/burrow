@file:OptIn(ExperimentalMaterial3Api::class)

package io.github.xattribution.burrow.ui.links

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.LinkOff
import androidx.compose.material.icons.outlined.QrCode2
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.WifiOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import io.github.xattribution.burrow.core.Share
import io.github.xattribution.burrow.core.ShareInput
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.ui.common.Busy
import io.github.xattribution.burrow.ui.common.EmptyState
import io.github.xattribution.burrow.ui.common.Load
import io.github.xattribution.burrow.ui.common.formatWhen
import io.github.xattribution.burrow.ui.common.friendly
import kotlinx.coroutines.launch

/** The links you made: copy, share, show as a QR code, turn off or delete. */
@Composable
fun LinksScreen() {
    val conn = LocalContext.current.graph.connection
    var state by remember { mutableStateOf<Load<List<Share>>>(Load.Loading) }
    var refreshing by remember { mutableStateOf(false) }
    var reload by remember { mutableStateOf(0) }
    LaunchedEffect(reload) {
        state = try {
            Load.Ready(conn.use { it.shares() }.filter { it.mode == "link" || it.mode == "collection" })
        } catch (e: Exception) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            Load.Failed(friendly(e))
        }
        refreshing = false
    }
    Scaffold(topBar = { TopAppBar(title = { Text("Links") }) }) { pad ->
        PullToRefreshBox(isRefreshing = refreshing, onRefresh = { refreshing = true; reload++ }, modifier = Modifier.padding(pad).fillMaxSize()) {
            when (val s = state) {
                is Load.Loading -> Busy()
                is Load.Failed -> EmptyState(Icons.Outlined.WifiOff, s.message, action = "Try again") { reload++ }
                is Load.Ready -> if (s.value.isEmpty()) {
                    EmptyState(Icons.Outlined.Link, "No links yet", "Open any file or folder in Files and choose Share link.")
                } else {
                    val active = s.value.filter { it.state == "active" }
                    val other = s.value.filter { it.state != "active" }
                    LazyColumn(Modifier.fillMaxSize()) {
                        items(active, key = { it.id }) { LinkRow(it, onChanged = { reload++ }, showPath = true) }
                        if (other.isNotEmpty()) item { Text("Not working", style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(16.dp)) }
                        items(other, key = { it.id }) { LinkRow(it, onChanged = { reload++ }, showPath = true) }
                    }
                }
            }
        }
    }
}

private val stateText = mapOf("active" to "Active", "expired" to "Expired", "revoked" to "Turned off", "used-up" to "Download limit reached", "missing" to "File is gone", "unavailable" to "You lost access")

@Composable
fun LinkRow(s: Share, onChanged: () -> Unit, showPath: Boolean = false) {
    val ctx = LocalContext.current
    val conn = ctx.graph.connection
    val scope = rememberCoroutineScope()
    var qr by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    val bits = buildList {
        add(stateText[s.state] ?: s.state)
        if (s.expires > 0 && s.state == "active") add("expires ${formatWhen(s.expires)}")
        if (s.hasPassword) add("password")
        if (s.maxDownloads > 0) add("${s.downloads}/${s.maxDownloads} downloads") else if (s.downloads > 0) add("${s.downloads} downloads")
    }
    ListItem(
        headlineContent = { Text(if (showPath) s.label.ifBlank { s.path.substringAfterLast('/') } else s.url.substringAfter("://"), maxLines = 1, overflow = TextOverflow.Ellipsis) },
        supportingContent = {
            Column {
                if (showPath) Text(s.url.substringAfter("://"), style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace), maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(bits.joinToString(" · "), maxLines = 2)
            }
        },
        leadingContent = { Icon(if (s.state == "active") Icons.Outlined.Link else Icons.Outlined.LinkOff, null, tint = if (s.state == "active") MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant) },
        trailingContent = {
            Row {
                IconButton(onClick = { copy(ctx, s.url) }) { Icon(Icons.Outlined.ContentCopy, "Copy link") }
                IconButton(onClick = { qr = true }) { Icon(Icons.Outlined.QrCode2, "Show QR code") }
                IconButton(onClick = { shareText(ctx, s.url) }) { Icon(Icons.Outlined.Share, "Share link") }
            }
        },
    )
    if (qr) {
        AlertDialog(
            onDismissRequest = { qr = false },
            title = { Text(s.label.ifBlank { s.path.substringAfterLast('/') }) },
            text = {
                Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    val bmp = remember(s.url) { qrBitmap(s.url, 640) }
                    Box(Modifier.background(Color.White).padding(12.dp)) { Image(bmp.asImageBitmap(), "QR code for the link", Modifier.size(240.dp)) }
                    Text(s.url, style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text("Link works", Modifier.weight(1f))
                        Switch(checked = !s.revoked, onCheckedChange = { on ->
                            scope.launch { runCatching { conn.use { it.setShareActive(s.id, on) } }; qr = false; onChanged() }
                        })
                    }
                }
            },
            confirmButton = { TextButton(onClick = { qr = false }) { Text("Done") } },
            dismissButton = { TextButton(onClick = { qr = false; confirmDelete = true }) { Text("Delete link") } },
        )
    }
    if (confirmDelete) {
        AlertDialog(
            onDismissRequest = { confirmDelete = false },
            title = { Text("Delete this link?") },
            text = { Text("People who have it can't open it any more. The file itself isn't touched.") },
            confirmButton = { TextButton(onClick = { confirmDelete = false; scope.launch { runCatching { conn.use { it.deleteShare(s.id) } }; onChanged() } }) { Text("Delete") } },
            dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text("Cancel") } },
        )
    }
}

/** Make a link for a file or folder: how long it works, a password, a download limit. */
@Composable
fun NewLinkSheet(path: String, onDismiss: () -> Unit, onCreated: () -> Unit) {
    val ctx = LocalContext.current
    val conn = ctx.graph.connection
    val scope = rememberCoroutineScope()
    var hours by remember { mutableStateOf(168.0) }
    var password by remember { mutableStateOf("") }
    var limit by remember { mutableStateOf("") }
    var made by remember { mutableStateOf<Share?>(null) }
    var busy by remember { mutableStateOf(false) }
    var err by remember { mutableStateOf<String?>(null) }
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 24.dp).navigationBarsPadding(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            val m = made
            if (m == null) {
                Text("Share ${path.substringAfterLast('/')}", style = MaterialTheme.typography.titleLarge)
                Text("Works for", style = MaterialTheme.typography.labelLarge)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    listOf(24.0 to "1 day", 168.0 to "7 days", 720.0 to "30 days", -1.0 to "Always").forEach { (h, l) ->
                        FilterChip(selected = hours == h, onClick = { hours = h }, label = { Text(l) })
                    }
                }
                OutlinedTextField(password, { password = it }, label = { Text("Password (optional)") }, singleLine = true, visualTransformation = PasswordVisualTransformation(), modifier = Modifier.fillMaxWidth())
                OutlinedTextField(limit, { limit = it.filter(Char::isDigit).take(6) }, label = { Text("Download limit (optional)") }, singleLine = true, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number), modifier = Modifier.fillMaxWidth())
                err?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                Button(enabled = !busy, modifier = Modifier.fillMaxWidth(), onClick = {
                    busy = true
                    scope.launch {
                        try {
                            made = conn.use { it.createShare(ShareInput(path = path, expiresHours = hours, password = password, maxDownloads = limit.toLongOrNull() ?: 0)) }
                            onCreated()
                        } catch (e: Exception) {
                            err = friendly(e)
                        } finally {
                            busy = false
                        }
                    }
                }) { Text("Create link") }
            } else {
                Text("Link ready", style = MaterialTheme.typography.titleLarge)
                Box(Modifier.align(Alignment.CenterHorizontally).background(Color.White).padding(12.dp)) {
                    Image(qrBitmap(m.url, 640).asImageBitmap(), "QR code for the link", Modifier.size(200.dp))
                }
                Text(m.url, style = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(onClick = { copy(ctx, m.url) }) { Icon(Icons.Outlined.ContentCopy, null); Text("Copy", Modifier.padding(start = 8.dp)) }
                    OutlinedButton(onClick = { shareText(ctx, m.url) }) { Icon(Icons.Outlined.Share, null); Text("Share", Modifier.padding(start = 8.dp)) }
                }
                TextButton(onClick = onDismiss, modifier = Modifier.align(Alignment.End)) { Text("Done") }
            }
        }
    }
}

fun copy(ctx: Context, text: String) {
    ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Burrow link", text))
}

fun shareText(ctx: Context, text: String) {
    ctx.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text), "Share link").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
}

fun qrBitmap(text: String, size: Int): Bitmap {
    val m = QRCodeWriter().encode(text, BarcodeFormat.QR_CODE, size, size, mapOf(EncodeHintType.MARGIN to 1))
    val px = IntArray(size * size) { i -> if (m[i % size, i / size]) android.graphics.Color.BLACK else android.graphics.Color.WHITE }
    return Bitmap.createBitmap(px, size, size, Bitmap.Config.ARGB_8888)
}
