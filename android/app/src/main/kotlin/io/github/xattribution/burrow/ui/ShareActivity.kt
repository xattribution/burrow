package io.github.xattribution.burrow.ui

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.ui.common.BurrowFolderPicker
import io.github.xattribution.burrow.ui.common.Centered
import io.github.xattribution.burrow.ui.theme.BurrowTheme
import io.github.xattribution.burrow.work.stageForUpload
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** "Save to Burrow" in other apps' share menus: pick a folder, the files go there. */
class ShareActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val uris = urisOf(intent)
        setContent {
            BurrowTheme {
                val scope = rememberCoroutineScope()
                var staging by remember { mutableStateOf(false) }
                when {
                    graph.device.current == null -> Centered {
                        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
                            Text("Pair Burrow first", style = MaterialTheme.typography.titleLarge)
                            Text("Open the Burrow app and scan the code from Burrow's Settings.", textAlign = TextAlign.Center)
                            Button(onClick = { startActivity(Intent(this@ShareActivity, MainActivity::class.java)); finish() }) { Text("Open Burrow") }
                        }
                    }
                    uris.isEmpty() -> Centered { Text("There was nothing to save.", Modifier.padding(24.dp)) }
                    staging -> Centered {
                        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
                            CircularProgressIndicator()
                            Text("Getting ${uris.size} file${if (uris.size == 1) "" else "s"} ready…")
                        }
                    }
                    else -> BurrowFolderPicker(
                        title = "Save ${uris.size} file${if (uris.size == 1) "" else "s"} to Burrow",
                        needWrite = true, confirm = "Save here",
                        onCancel = { finish() },
                        onPick = { dir ->
                            staging = true
                            scope.launch {
                                // copied now, while this app may still read them; sent in the background
                                withContext(Dispatchers.IO) { uris.forEach { graph.transfers.upload(stageForUpload(this@ShareActivity, it), dir) } }
                                Toast.makeText(this@ShareActivity, "Sending to $dir", Toast.LENGTH_SHORT).show()
                                finish()
                            }
                        },
                    )
                }
            }
        }
    }

    @Suppress("DEPRECATION")
    private fun urisOf(i: Intent): List<Uri> = when (i.action) {
        Intent.ACTION_SEND -> listOfNotNull(if (Build.VERSION.SDK_INT >= 33) i.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java) else i.getParcelableExtra(Intent.EXTRA_STREAM))
        Intent.ACTION_SEND_MULTIPLE -> (if (Build.VERSION.SDK_INT >= 33) i.getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java) else i.getParcelableArrayListExtra(Intent.EXTRA_STREAM)).orEmpty()
        else -> emptyList()
    }
}
