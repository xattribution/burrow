package io.github.xattribution.burrow.ui.files

import android.content.ActivityNotFoundException
import android.content.Intent
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalContext
import io.github.xattribution.burrow.data.PhoneAccess
import io.github.xattribution.burrow.graph

/**
 * Asks for access to the phone's files: the "All files access" switch in the
 * system settings on Android 11 and later, the storage permission on Android 10.
 * [onResult] runs when the person comes back, with whether it is on now.
 */
@Composable
fun rememberPhoneAccessRequest(onResult: (Boolean) -> Unit): () -> Unit {
    val ctx = LocalContext.current
    val settings = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { onResult(ctx.graph.phoneAccess()) }
    val legacy = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { onResult(ctx.graph.phoneAccess()) }
    return {
        if (Build.VERSION.SDK_INT >= 30) {
            try {
                settings.launch(PhoneAccess.settingsIntent(ctx))
            } catch (e: ActivityNotFoundException) {
                // some phones only have the list of all apps
                settings.launch(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
            }
        } else {
            legacy.launch(PhoneAccess.legacyPermissions)
        }
    }
}
