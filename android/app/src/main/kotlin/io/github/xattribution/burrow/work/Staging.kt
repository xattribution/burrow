package io.github.xattribution.burrow.work

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import java.io.File

/**
 * Files handed over by another app (share sheet, drag and drop) come with access
 * that ends when this app's screen closes, but the upload may take longer. So
 * they are copied into the app's cache first; the copy is deleted once sent.
 */
fun stageForUpload(context: Context, uri: Uri): Uri {
    if (uri.scheme == "file") return uri
    val cr = context.contentResolver
    var name = "shared-${System.currentTimeMillis()}"
    cr.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c -> if (c.moveToFirst() && !c.isNull(0)) name = c.getString(0) }
    val dir = File(context.cacheDir, "outbox/${System.nanoTime()}").apply { mkdirs() }
    val out = File(dir, name.replace('/', '_'))
    (cr.openInputStream(uri) ?: return uri).use { input -> out.outputStream().use { input.copyTo(it) } }
    return Uri.fromFile(out)
}
