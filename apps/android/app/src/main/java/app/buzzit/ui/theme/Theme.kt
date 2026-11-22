package app.buzzit.ui.theme

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

val BuzzAmber = Color(0xFFFFB300)
val BuzzRed = Color(0xFFD32F2F)

private val Light = lightColorScheme(
    primary = Color(0xFF7A5900), onPrimary = Color.White,
    primaryContainer = Color(0xFFFFDEA6), onPrimaryContainer = Color(0xFF261900),
    secondary = Color(0xFF6B5D3F), secondaryContainer = Color(0xFFF4E0BB),
    tertiary = Color(0xFF4A6547), background = Color(0xFFFFFBFF), surface = Color(0xFFFFFBFF),
)

private val Dark = darkColorScheme(
    primary = Color(0xFFF9BC3D), onPrimary = Color(0xFF402D00),
    primaryContainer = Color(0xFF5C4200), onPrimaryContainer = Color(0xFFFFDEA6),
    secondary = Color(0xFFD7C4A0), secondaryContainer = Color(0xFF524529),
    tertiary = Color(0xFFB0CFAA), background = Color(0xFF1E1B16), surface = Color(0xFF1E1B16),
)

@Composable
fun BuzzitTheme(dynamic: Boolean = false, content: @Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    val scheme = when {
        dynamic && Build.VERSION.SDK_INT >= 31 -> if (dark) dynamicDarkColorScheme(LocalContext.current) else dynamicLightColorScheme(LocalContext.current)
        dark -> Dark
        else -> Light
    }
    MaterialTheme(colorScheme = scheme, content = content)
}
