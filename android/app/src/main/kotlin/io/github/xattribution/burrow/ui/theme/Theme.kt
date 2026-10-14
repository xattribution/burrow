package io.github.xattribution.burrow.ui.theme

import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext

// Amber, matching the web UI; used only where the phone has no wallpaper colors (Android 11 and older).
private val Light = lightColorScheme(
    primary = Color(0xFF8A5A00), onPrimary = Color.White,
    primaryContainer = Color(0xFFFFDDB0), onPrimaryContainer = Color(0xFF2C1A00),
    secondary = Color(0xFF6E5B40), secondaryContainer = Color(0xFFF8DFBB), onSecondaryContainer = Color(0xFF261904),
    tertiary = Color(0xFF50643F), background = Color(0xFFFFFBF7), surface = Color(0xFFFFFBF7),
    surfaceVariant = Color(0xFFEFE0CF), onSurfaceVariant = Color(0xFF4F4539), outline = Color(0xFF817567),
    error = Color(0xFFBA1A1A),
)
private val Dark = darkColorScheme(
    primary = Color(0xFFF5C24A), onPrimary = Color(0xFF3F2C00),
    primaryContainer = Color(0xFF5B4300), onPrimaryContainer = Color(0xFFFFDF9A),
    secondary = Color(0xFFDBC4A0), secondaryContainer = Color(0xFF52452F), onSecondaryContainer = Color(0xFFF8DFBB),
    tertiary = Color(0xFFB5CEA1), surfaceVariant = Color(0xFF3A3530), onSurfaceVariant = Color(0xFFD3C4B4),
    outline = Color(0xFF9C8F80), error = Color(0xFFFFB4AB),
)

/**
 * True black for OLED screens: the background is pixels-off black and the raised
 * surfaces (sheets, bars, menus) are neutral near-blacks, so the wallpaper accent is
 * the only color on screen. The accent and its containers stay as the scheme made them.
 */
private fun ColorScheme.oled(): ColorScheme = copy(
    background = Color.Black,
    surface = Color.Black,
    surfaceDim = Color.Black,
    surfaceContainerLowest = Color.Black,
    surfaceContainerLow = Color(0xFF0D0D0E),
    surfaceContainer = Color(0xFF121214),
    surfaceContainerHigh = Color(0xFF1B1B1E),
    surfaceContainerHighest = Color(0xFF242428),
    surfaceBright = Color(0xFF2B2B2F),
    surfaceVariant = Color(0xFF242428),
    outlineVariant = Color(0xFF2E2E33),
    onBackground = Color(0xFFF2F2F2),
    onSurface = Color(0xFFF2F2F2),
)

@Composable
fun BurrowTheme(dark: Boolean = isSystemInDarkTheme(), dynamic: Boolean = true, content: @Composable () -> Unit) {
    val ctx = LocalContext.current
    val scheme = when {
        dynamic && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> if (dark) dynamicDarkColorScheme(ctx).oled() else dynamicLightColorScheme(ctx)
        dark -> Dark.oled()
        else -> Light
    }
    MaterialTheme(colorScheme = scheme, content = content)
}
