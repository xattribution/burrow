package io.github.xattribution.burrow.ui.pair

import android.Manifest
import android.content.ClipboardManager
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.ui.res.colorResource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ContentPaste
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import io.github.xattribution.burrow.R
import io.github.xattribution.burrow.core.PairingLink
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.ui.common.friendly
import kotlinx.coroutines.launch

/** First run: pair this phone with Burrow by scanning the code from the web UI. */
@Composable
fun PairScreen(initialLink: String?, onLinkUsed: () -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var link by rememberSaveable { mutableStateOf<String?>(null) }
    var scanning by rememberSaveable { mutableStateOf(false) }
    var pasting by rememberSaveable { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    var name by rememberSaveable { mutableStateOf(Build.MODEL) }

    LaunchedEffect(initialLink) {
        if (initialLink != null) {
            link = initialLink
            onLinkUsed()
        }
    }
    val camera = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (ok) scanning = true else error = "Burrow needs the camera to scan the code. You can paste the pairing link instead."
    }
    val parsed = remember(link) { link?.let { runCatching { PairingLink.parse(it) } } }

    Surface(Modifier.fillMaxSize()) {
        Box(Modifier.fillMaxSize().safeDrawingPadding(), contentAlignment = Alignment.TopCenter) {
            Column(
                Modifier.widthIn(max = 520.dp).fillMaxWidth().verticalScroll(rememberScrollState()).padding(24.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                AppMark(Modifier.size(72.dp))
                Text("Pair with Burrow", style = MaterialTheme.typography.headlineSmall)
                when {
                    parsed?.isSuccess == true -> {
                        val l = parsed.getOrThrow()
                        Text("This phone will reach your Burrow at:", style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center)
                        Card(Modifier.fillMaxWidth()) {
                            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                                Address("At home", l.lan ?: "not set")
                                Address("Away", l.public ?: "not set")
                            }
                        }
                        OutlinedTextField(name, { name = it }, label = { Text("Name for this phone") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            Icon(Icons.Outlined.Lock, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.primary)
                            Text(
                                "Everything between this phone and Burrow is end-to-end encrypted with post-quantum keys. A reverse proxy in between only sees scrambled data.",
                                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        if (busy) CircularProgressIndicator()
                        else Button(onClick = {
                            busy = true
                            error = null
                            scope.launch {
                                try {
                                    ctx.graph.connection.pair(l, name)
                                } catch (e: Exception) {
                                    error = friendly(e)
                                } finally {
                                    busy = false
                                }
                            }
                        }, modifier = Modifier.fillMaxWidth()) { Text("Pair") }
                        TextButton(onClick = { link = null; error = null }) { Text("Use a different code") }
                    }
                    scanning -> {
                        Text("Point the camera at the code in Burrow's Settings, under Phones.", textAlign = TextAlign.Center)
                        QrScanner(onCode = { link = it; scanning = false }, modifier = Modifier.fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(16.dp)))
                        TextButton(onClick = { scanning = false }) { Text("Cancel") }
                    }
                    pasting -> {
                        var text by remember { mutableStateOf("") }
                        Text("Paste the link shown under “Can’t scan?” in Burrow.", textAlign = TextAlign.Center)
                        OutlinedTextField(text, { text = it }, label = { Text("Pairing link") }, modifier = Modifier.fillMaxWidth(), textStyle = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace))
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            OutlinedButton(onClick = {
                                val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                                text = cm.primaryClip?.getItemAt(0)?.coerceToText(ctx)?.toString().orEmpty()
                            }) { Text("Paste") }
                            Button(onClick = { link = text.trim(); pasting = false }, enabled = text.isNotBlank()) { Text("Continue") }
                        }
                        TextButton(onClick = { pasting = false }) { Text("Cancel") }
                    }
                    else -> {
                        Text(
                            "On a computer, open Burrow, go to Settings, find Phones and choose Pair a phone. Then scan the code it shows.",
                            style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center,
                        )
                        Button(onClick = {
                            if (ctx.checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) scanning = true
                            else camera.launch(Manifest.permission.CAMERA)
                        }, modifier = Modifier.fillMaxWidth()) {
                            Icon(Icons.Outlined.QrCodeScanner, null)
                            Text("Scan the code", Modifier.padding(start = 8.dp))
                        }
                        OutlinedButton(onClick = { pasting = true }, modifier = Modifier.fillMaxWidth()) {
                            Icon(Icons.Outlined.ContentPaste, null)
                            Text("Paste a pairing link", Modifier.padding(start = 8.dp))
                        }
                    }
                }
                if (parsed?.isFailure == true) Text(parsed.exceptionOrNull()?.message ?: "That isn't a Burrow pairing code.", color = MaterialTheme.colorScheme.error, textAlign = TextAlign.Center)
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, textAlign = TextAlign.Center) }
            }
        }
    }
}

/** The app icon, drawn from its layers (an adaptive icon can't be painted directly). */
@Composable
fun AppMark(modifier: Modifier = Modifier) {
    Box(modifier.clip(RoundedCornerShape(22)).background(colorResource(R.color.icon_background)), contentAlignment = Alignment.Center) {
        Image(painterResource(R.drawable.ic_launcher_foreground), null, Modifier.fillMaxSize())
    }
}

@Composable
private fun Address(label: String, value: String) {
    Column {
        Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, style = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace))
    }
}
