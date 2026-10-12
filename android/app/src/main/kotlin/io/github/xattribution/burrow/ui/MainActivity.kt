package io.github.xattribution.burrow.ui

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.mutableStateOf
import io.github.xattribution.burrow.ui.theme.BurrowTheme

class MainActivity : ComponentActivity() {
    /** A burrow://pair link this activity was opened with (from a QR scanned in another app). */
    private val pairLink = mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        pairLink.value = linkOf(intent)
        setContent {
            BurrowTheme {
                BurrowUi(pairLink = pairLink.value, onLinkUsed = { pairLink.value = null })
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        linkOf(intent)?.let { pairLink.value = it }
    }

    private fun linkOf(i: Intent?) = i?.data?.takeIf { it.scheme == "burrow" && it.host == "pair" }?.toString()
}
