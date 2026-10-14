@file:OptIn(ExperimentalMaterial3Api::class, ExperimentalMaterial3AdaptiveApi::class, ExperimentalLayoutApi::class)

package io.github.xattribution.burrow.ui.files

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.pdf.PdfRenderer
import android.os.ParcelFileDescriptor
import android.webkit.MimeTypeMap
import androidx.annotation.OptIn as AndroidOptIn
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.rememberTransformableState
import androidx.compose.foundation.gestures.transformable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.DriveFileRenameOutline
import androidx.compose.material.icons.outlined.FileDownload
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.Star
import androidx.compose.material.icons.outlined.StarBorder
import androidx.compose.material.icons.outlined.WifiOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.adaptive.ExperimentalMaterial3AdaptiveApi
import androidx.compose.material3.adaptive.currentWindowAdaptiveInfo
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.FileProvider
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import coil3.compose.AsyncImage
import io.github.xattribution.burrow.core.Info
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.media.ChannelDataSource
import io.github.xattribution.burrow.data.BurrowFs
import io.github.xattribution.burrow.data.Place
import io.github.xattribution.burrow.data.RemoteFileProvider
import io.github.xattribution.burrow.core.Entry
import androidx.compose.material.icons.outlined.FileCopy
import androidx.compose.material.icons.automirrored.outlined.DriveFileMove
import io.github.xattribution.burrow.media.FileImageFetcher
import io.github.xattribution.burrow.ui.common.Busy
import io.github.xattribution.burrow.ui.common.EmptyState
import io.github.xattribution.burrow.ui.common.Load
import io.github.xattribution.burrow.ui.common.formatSize
import io.github.xattribution.burrow.ui.common.friendly
import io.github.xattribution.burrow.ui.common.kindIcon
import io.github.xattribution.burrow.ui.links.LinkRow
import io.github.xattribution.burrow.ui.links.NewLinkSheet
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okio.buffer
import okio.sink
import java.io.File
import java.text.DateFormat
import java.util.Date

@Composable
fun DetailPane(
    path: String,
    place: String = Place.HOME,
    showBack: Boolean,
    onBack: () -> Unit,
    onChanged: () -> Unit,
    onOpenFolder: (String) -> Unit,
    onRenamed: (String) -> Unit,
    onDeleted: () -> Unit,
) {
    val ctx = LocalContext.current
    val fs = remember(place) { ctx.graph.places.fs(place) }
    val burrow = fs as? BurrowFs
    val home = place == Place.HOME
    val scope = rememberCoroutineScope()
    val snack = remember { SnackbarHostState() }
    var copying by remember { mutableStateOf<Boolean?>(null) } // true: move, false: copy
    var info by remember(path) { mutableStateOf<Load<Info>>(Load.Loading) }
    var reload by remember { mutableStateOf(0) }
    var menu by remember { mutableStateOf(false) }
    var renaming by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf(false) }
    var sharing by remember { mutableStateOf(false) }
    var opening by remember { mutableStateOf<Float?>(null) }

    LaunchedEffect(path, reload) {
        info = try {
            Load.Ready(burrow?.conn?.use { it.info(path) } ?: fs.stat(path).let { e -> Info(entry = e, canRead = true, canChange = true, preview = previewOf(e)) })
        } catch (e: Exception) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            Load.Failed(friendly(e))
        }
    }
    val name = path.substringAfterLast('/')
    val i = (info as? Load.Ready)?.value

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = { if (showBack) IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                title = { Text(name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                actions = {
                    if (i != null) {
                        if (burrow != null) IconButton(onClick = {
                            scope.launch {
                                runCatching { burrow.conn.use { it.setFavorite(path, !i.entry.fav) } }
                                reload++
                                onChanged()
                            }
                        }) { Icon(if (i.entry.fav) Icons.Outlined.Star else Icons.Outlined.StarBorder, if (i.entry.fav) "Remove from favorites" else "Add to favorites") }
                        IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More") }
                        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                            DropdownMenuItem(text = { Text("Show in folder") }, leadingIcon = { Icon(Icons.Outlined.Folder, null) }, onClick = { menu = false; onOpenFolder(path.substringBeforeLast('/', "")) })
                            DropdownMenuItem(text = { Text("Copy to…") }, leadingIcon = { Icon(Icons.Outlined.FileCopy, null) }, onClick = { menu = false; copying = false })
                            if (i.canChange) {
                                DropdownMenuItem(text = { Text("Move to…") }, leadingIcon = { Icon(Icons.AutoMirrored.Outlined.DriveFileMove, null) }, onClick = { menu = false; copying = true })
                                DropdownMenuItem(text = { Text("Rename") }, leadingIcon = { Icon(Icons.Outlined.DriveFileRenameOutline, null) }, onClick = { menu = false; renaming = true })
                                DropdownMenuItem(text = { Text("Delete") }, leadingIcon = { Icon(Icons.Outlined.Delete, null) }, onClick = { menu = false; deleting = true })
                            }
                        }
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snack) },
    ) { pad ->
        when (val l = info) {
            is Load.Loading -> Box(Modifier.padding(pad)) { Busy() }
            is Load.Failed -> Box(Modifier.padding(pad)) { EmptyState(Icons.Outlined.WifiOff, l.message, action = "Try again") { reload++ } }
            is Load.Ready -> {
                val d = l.value
                // a foldable half-open on a table: the picture or video on the top half, the rest below the fold
                val tabletop = currentWindowAdaptiveInfo().windowPosture.isTabletop && d.preview in setOf("image", "video")
                val details: @Composable () -> Unit = {
                    Facts(d)
                    FlowRow(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        if (d.canShare && home) Button(onClick = { sharing = true }) { Icon(Icons.Outlined.Link, null); Text("Share link", Modifier.padding(start = 8.dp)) }
                        OutlinedButton(onClick = {
                            ctx.graph.transfers.download(path, d.entry.size, place)
                            scope.launch { snack.showSnackbar("Saving to Downloads/Burrow") }
                        }) { Icon(Icons.Outlined.FileDownload, null); Text("Save to phone", Modifier.padding(start = 8.dp)) }
                        OutlinedButton(enabled = opening == null, onClick = {
                            scope.launch {
                                opening = 0f
                                try {
                                    openWith(ctx, place, d.entry)
                                } catch (e: Exception) {
                                    snack.showSnackbar(friendly(e))
                                } finally {
                                    opening = null
                                }
                            }
                        }) { Icon(Icons.AutoMirrored.Outlined.OpenInNew, null); Text("Open with", Modifier.padding(start = 8.dp)) }
                    }
                    opening?.let { LinearProgressIndicator(progress = { it }, modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp)) }
                    if (d.shares.isNotEmpty() && home) {
                        HorizontalDivider(Modifier.padding(vertical = 8.dp))
                        Text("Links", style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(horizontal = 16.dp))
                        d.shares.forEach { s -> LinkRow(s, onChanged = { reload++ }) }
                    }
                }
                if (tabletop) {
                    Column(Modifier.padding(pad).fillMaxSize()) {
                        Box(Modifier.weight(1f).fillMaxWidth()) { Preview(d, place, Modifier.fillMaxSize()) }
                        Column(Modifier.weight(1f).verticalScroll(rememberScrollState())) { details() }
                    }
                } else {
                    Column(Modifier.padding(pad).fillMaxSize().verticalScroll(rememberScrollState())) {
                        Preview(d, place, Modifier.fillMaxWidth().heightIn(max = 480.dp))
                        details()
                    }
                }
            }
        }
    }

    copying?.let { move ->
        if (i != null) PlaceTargetPicker(
            title = (if (move) "Move " else "Copy ") + name,
            confirm = if (move) "Move here" else "Copy here",
            onDismiss = { copying = null },
            onPick = { dst, dir ->
                copying = null
                scope.launch {
                    try {
                        if (dst == place && move) {
                            onRenamed(fs.move(path, (if (dir.isEmpty()) "" else "$dir/") + name))
                        } else {
                            ctx.graph.transfers.copy(place, i.entry, dst, dir, move)
                            snack.showSnackbar((if (move) "Moving " else "Copying ") + "$name to ${ctx.graph.places.get(dst)?.name ?: "Burrow"}")
                        }
                    } catch (e: Exception) {
                        snack.showSnackbar(friendly(e))
                    }
                }
            },
        )
    }
    if (sharing && i != null) NewLinkSheet(path = path, onDismiss = { sharing = false }, onCreated = { reload++; onChanged() })
    if (renaming && i != null) {
        var n by remember { mutableStateOf(name) }
        var err by remember { mutableStateOf<String?>(null) }
        AlertDialog(
            onDismissRequest = { renaming = false },
            title = { Text("Rename") },
            text = {
                Column {
                    OutlinedTextField(n, { n = it }, singleLine = true, isError = err != null, supportingText = err?.let { { Text(it) } })
                }
            },
            confirmButton = {
                TextButton(enabled = n.isNotBlank() && n != name, onClick = {
                    scope.launch {
                        try {
                            val np = fs.move(path, path.substringBeforeLast('/', "").let { if (it.isEmpty() && !fs.isBurrow) "" else "$it/" } + n.trim())
                            renaming = false
                            onRenamed(np)
                        } catch (e: Exception) {
                            err = friendly(e)
                        }
                    }
                }) { Text("Rename") }
            },
            dismissButton = { TextButton(onClick = { renaming = false }) { Text("Cancel") } },
        )
    }
    if (deleting && i != null) {
        AlertDialog(
            onDismissRequest = { deleting = false },
            title = { Text("Delete $name?") },
            text = { Text(if (fs.isBurrow) "It goes to Burrow's trash and can be restored for 30 days." else "This share has no trash, so it's gone for good.") },
            confirmButton = {
                TextButton(onClick = {
                    deleting = false
                    scope.launch {
                        try {
                            fs.delete(path)
                            onDeleted()
                        } catch (e: Exception) {
                            snack.showSnackbar(friendly(e))
                        }
                    }
                }) { Text("Delete") }
            },
            dismissButton = { TextButton(onClick = { deleting = false }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun Facts(d: Info) {
    val e = d.entry
    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(e.name, style = MaterialTheme.typography.titleMedium)
        val bits = buildList {
            if (e.dir) add("${d.items} items, ${formatSize(d.totalSize)}") else add(formatSize(e.size))
            add("Modified " + DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT).format(Date(e.mtime * 1000)))
        }
        Text(bits.joinToString(" · "), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(e.path.substringBeforeLast('/', ""), style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace), color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun Preview(d: Info, place: String, modifier: Modifier) {
    val e = d.entry
    when (d.preview) {
        "image" -> ZoomableImage(e.path, e.mtime, place, modifier)
        "video", "audio" -> MediaPlayer(e.path, place, modifier)
        "text" -> TextPreview(e.path, place, modifier)
        "pdf" -> PdfPreview(e.path, e.size, place, modifier)
        else -> Box(modifier.heightIn(min = 160.dp), contentAlignment = Alignment.Center) {
            Icon(kindIcon(e.kind, e.dir), null, Modifier.size(72.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun ZoomableImage(path: String, mtime: Long, place: String, modifier: Modifier) {
    var scale by remember { mutableFloatStateOf(1f) }
    var offset by remember { mutableStateOf(androidx.compose.ui.geometry.Offset.Zero) }
    val state = rememberTransformableState { zoom, pan, _ ->
        scale = (scale * zoom).coerceIn(1f, 6f)
        offset = if (scale == 1f) androidx.compose.ui.geometry.Offset.Zero else offset + pan
    }
    Box(modifier.background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.3f)).transformable(state), contentAlignment = Alignment.Center) {
        AsyncImage(
            model = FileImageFetcher.Image(path, mtime, place), contentDescription = null, contentScale = ContentScale.Fit,
            modifier = Modifier.fillMaxWidth().graphicsLayer(scaleX = scale, scaleY = scale, translationX = offset.x, translationY = offset.y),
        )
    }
}

@AndroidOptIn(UnstableApi::class)
@Composable
private fun MediaPlayer(path: String, place: String, modifier: Modifier) {
    val ctx = LocalContext.current
    val player = remember(path) {
        ExoPlayer.Builder(ctx).setMediaSourceFactory(DefaultMediaSourceFactory(ChannelDataSource.Factory(ctx.graph.places))).build().apply {
            setMediaItem(MediaItem.fromUri(ChannelDataSource.uriFor(path, place)))
            prepare()
        }
    }
    DisposableEffect(player) { onDispose { player.release() } }
    AndroidView(factory = { PlayerView(it).apply { this.player = player } }, modifier = modifier.heightIn(min = 220.dp))
}

@Composable
private fun TextPreview(path: String, place: String, modifier: Modifier) {
    val fs = LocalContext.current.graph.places.fs(place)
    var text by remember(path) { mutableStateOf<String?>(null) }
    LaunchedEffect(path) {
        text = runCatching { fs.open(path).use { o -> withContext(Dispatchers.IO) { val b = okio.Buffer(); o.source.read(b, 256 * 1024); b.readUtf8() } } }.getOrElse { friendly(it) }
    }
    SelectionContainer {
        Text(
            text ?: "Loading…", style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
            modifier = modifier.background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.3f)).padding(12.dp).verticalScroll(rememberScrollState()),
        )
    }
}

@Composable
private fun PdfPreview(path: String, size: Long, place: String, modifier: Modifier) {
    val ctx = LocalContext.current
    var pages by remember(path) { mutableStateOf<List<Bitmap>?>(null) }
    var progress by remember { mutableStateOf<Float?>(null) }
    var err by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val p = pages
    if (p == null) {
        Column(modifier.padding(16.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (progress != null) LinearProgressIndicator(progress = { progress!! }, modifier = Modifier.fillMaxWidth())
            else Button(onClick = {
                scope.launch {
                    progress = 0f
                    try {
                        val f = fetchToCache(ctx, path, size, place) { progress = it }
                        pages = withContext(Dispatchers.IO) { renderPdf(f, ctx.resources.displayMetrics.widthPixels) }
                    } catch (e: Exception) {
                        err = friendly(e)
                    } finally {
                        progress = null
                    }
                }
            }) { Text("Show the PDF") }
            err?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        }
    } else {
        LazyColumn(modifier.heightIn(max = 900.dp)) {
            items(p) { bmp -> Image(bmp.asImageBitmap(), null, Modifier.fillMaxWidth().padding(vertical = 4.dp), contentScale = ContentScale.FillWidth) }
        }
    }
}

/** Renders up to 50 pages at screen width. */
private fun renderPdf(f: File, width: Int): List<Bitmap> {
    ParcelFileDescriptor.open(f, ParcelFileDescriptor.MODE_READ_ONLY).use { fd ->
        PdfRenderer(fd).use { r ->
            return (0 until minOf(r.pageCount, 50)).map { i ->
                r.openPage(i).use { page ->
                    val h = (width.toFloat() * page.height / page.width).toInt().coerceAtLeast(1)
                    Bitmap.createBitmap(width, h, Bitmap.Config.ARGB_8888).also {
                        it.eraseColor(android.graphics.Color.WHITE)
                        page.render(it, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY)
                    }
                }
            }
        }
    }
}

/** Downloads a file into the app's cache (cleared by the system as needed). */
suspend fun fetchToCache(ctx: Context, path: String, size: Long, place: String = Place.HOME, progress: (Float) -> Unit): File = withContext(Dispatchers.IO) {
    val dir = File(ctx.cacheDir, "open").apply { mkdirs() }
    val out = File(dir, path.substringAfterLast('/'))
    ctx.graph.places.fs(place).open(path).use { r ->
        out.sink().buffer().use { sink ->
            var got = 0L
            val buf = okio.Buffer()
            while (true) {
                val n = r.source.read(buf, 256 * 1024)
                if (n < 0) break
                sink.write(buf, n)
                got += n
                if (size > 0) progress(got.toFloat() / size)
            }
        }
    }
    out
}

/**
 * Hands the file to another app (a PDF reader, a 3D viewer, a video player).
 * It reads the file straight from where it is, through [RemoteFileProvider],
 * so nothing has to download first.
 */
fun openWith(ctx: Context, place: String, e: Entry) {
    val uri = RemoteFileProvider.uriFor(ctx, e, place)
    val i = Intent(Intent.ACTION_VIEW).setDataAndType(uri, RemoteFileProvider.mimeOf(e.name)).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
    try {
        ctx.startActivity(Intent.createChooser(i, "Open ${e.name} with").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    } catch (x: ActivityNotFoundException) {
        throw IllegalStateException("No app on this phone opens .${e.ext} files.")
    }
}

/** What Burrow would preview a file as; for places other than a Burrow. */
fun previewOf(e: Entry): String = when {
    e.dir -> ""
    e.ext in setOf("jpg", "jpeg", "png", "gif", "webp", "bmp", "heic", "heif", "avif") -> "image"
    e.kind == "video" -> "video"
    e.kind == "audio" -> "audio"
    e.ext == "pdf" -> "pdf"
    e.ext in setOf("txt", "md", "csv", "log", "json", "yaml", "yml", "toml", "ini", "conf", "xml", "sh", "py", "go", "kt", "js") && e.size < 2 shl 20 -> "text"
    else -> ""
}
