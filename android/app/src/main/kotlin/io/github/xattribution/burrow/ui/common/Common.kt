package io.github.xattribution.burrow.ui.common

import android.text.format.DateUtils
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.InsertDriveFile
import androidx.compose.material.icons.outlined.Archive
import androidx.compose.material.icons.outlined.AudioFile
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.ViewInAr
import androidx.compose.material.icons.outlined.VideoFile
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import io.github.xattribution.burrow.core.AccountDisabledException
import io.github.xattribution.burrow.core.ApiException
import io.github.xattribution.burrow.core.NotPairedException
import io.github.xattribution.burrow.core.UnreachableException
import java.util.Locale

/** A value that is loading, loaded, or failed to load. */
sealed interface Load<out T> {
    data object Loading : Load<Nothing>
    data class Ready<T>(val value: T) : Load<T>
    data class Failed(val message: String) : Load<Nothing>
}

fun friendly(e: Throwable): String = when (e) {
    is UnreachableException -> "Can't reach Burrow. Check your connection."
    is NotPairedException -> "This phone was removed from Burrow."
    is AccountDisabledException -> e.message ?: "Your Burrow account is turned off."
    is ApiException -> e.message ?: "Burrow said no (${e.status})."
    else -> e.message ?: "Something went wrong."
}

fun formatSize(n: Long): String {
    if (n < 1024) return "$n B"
    val units = listOf("KB", "MB", "GB", "TB", "PB")
    var v = n.toDouble()
    var i = -1
    do { v /= 1024; i++ } while (v >= 1024 && i < units.size - 1)
    return if (v >= 100) String.format(Locale.US, "%.0f %s", v, units[i]) else String.format(Locale.US, "%.1f %s", v, units[i])
}

/** "5 minutes ago", "Oct 3" and so on, from unix seconds. */
fun formatWhen(seconds: Long): String = when {
    seconds <= 0 -> "never"
    kotlin.math.abs(System.currentTimeMillis() / 1000 - seconds) < 60 -> "just now"
    else -> DateUtils.getRelativeTimeSpanString(seconds * 1000, System.currentTimeMillis(), DateUtils.MINUTE_IN_MILLIS).toString()
}

fun kindIcon(kind: String, dir: Boolean): ImageVector = when {
    dir -> Icons.Outlined.Folder
    kind == "image" -> Icons.Outlined.Image
    kind == "video" -> Icons.Outlined.VideoFile
    kind == "audio" -> Icons.Outlined.AudioFile
    kind == "doc" -> Icons.Outlined.Description
    kind == "archive" -> Icons.Outlined.Archive
    kind == "code" -> Icons.Outlined.Code
    kind == "3d" -> Icons.Outlined.ViewInAr
    else -> Icons.AutoMirrored.Outlined.InsertDriveFile
}

@Composable
fun Centered(content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) { content() }
}

@Composable
fun Busy() = Centered { CircularProgressIndicator() }

@Composable
fun EmptyState(icon: ImageVector, title: String, body: String? = null, action: String? = null, onAction: () -> Unit = {}) = Centered {
    Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Icon(icon, null, Modifier.size(48.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(title, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
        if (body != null) Text(body, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth())
        if (action != null) Button(onClick = onAction, modifier = Modifier.padding(top = 8.dp)) { Text(action) }
    }
}
