package io.github.xattribution.burrow.ui.theme

import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext

// Burrow's teal, from the web UI; used where the phone has no wallpaper colors (Android 11 and older).
private val Light = lightColorScheme(
    primary = Color(0xFF1F6F6D), onPrimary = Color.White,
    primaryContainer = Color(0xFFB9ECE8), onPrimaryContainer = Color(0xFF00201F),
    secondary = Color(0xFF4A6362), secondaryContainer = Color(0xFFCCE8E6), onSecondaryContainer = Color(0xFF051F1F),
    tertiary = Color(0xFF4A5F7C), background = Color(0xFFF7FAFA), surface = Color(0xFFF7FAFA),
    surfaceVariant = Color(0xFFDAE5E3), onSurfaceVariant = Color(0xFF3F4948), outline = Color(0xFF6F7978),
    error = Color(0xFFBA1A1A),
)
private val Dark = darkColorScheme(
    primary = Color(0xFF7FD6D1), onPrimary = Color(0xFF003735),
    primaryContainer = Color(0xFF1F504E), onPrimaryContainer = Color(0xFFB9ECE8),
    secondary = Color(0xFFB0CCCA), secondaryContainer = Color(0xFF324B4A), onSecondaryContainer = Color(0xFFCCE8E6),
    tertiary = Color(0xFFB2C7E9), background = Color(0xFF121718), surface = Color(0xFF121718),
    surfaceVariant = Color(0xFF3F4948), onSurfaceVariant = Color(0xFFBEC9C7), outline = Color(0xFF889391),
    error = Color(0xFFFFB4AB),
)

@Composable
fun BurrowTheme(dark: Boolean = isSystemInDarkTheme(), dynamic: Boolean = true, content: @Composable () -> Unit) {
    val ctx = LocalContext.current
    val scheme = when {
        dynamic && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> if (dark) dynamicDarkColorScheme(ctx) else dynamicLightColorScheme(ctx)
        dark -> Dark
        else -> Light
    }
    MaterialTheme(colorScheme = scheme, content = content)
}
