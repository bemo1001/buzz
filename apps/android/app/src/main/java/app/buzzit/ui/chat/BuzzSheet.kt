package app.buzzit.ui.chat

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import app.buzzit.ui.theme.BuzzAmber
import app.buzzit.ui.theme.BuzzRed

private data class LevelInfo(val wire: String, val title: String, val detail: String, val rank: Int)

private val levels = listOf(
    LevelInfo("nudge", "Nudge", "A gentle notification and a short vibration.", 1),
    LevelInfo("buzz", "Buzz", "Loud sound and strong vibration for 15 seconds — even on silent.", 2),
    LevelInfo("emergency", "Emergency", "Rings until they answer, through silent and Do Not Disturb.", 3),
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BuzzSheet(name: String, allowed: String, onDismiss: () -> Unit, onSend: (level: String, note: String) -> Unit) {
    val allowedRank = levels.firstOrNull { it.wire == allowed }?.rank ?: 1
    var level by remember { mutableStateOf(levels.first { it.rank == allowedRank }.wire) }
    var note by remember { mutableStateOf("") }
    var sending by remember { mutableStateOf(false) }

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(horizontal = 20.dp).padding(bottom = 32.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("Buzz $name", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
            levels.forEach { l ->
                val enabled = l.rank <= allowedRank
                val selected = level == l.wire
                val color = if (l.wire == "emergency") BuzzRed else BuzzAmber
                Surface(
                    shape = RoundedCornerShape(14.dp),
                    border = BorderStroke(if (selected) 2.dp else 1.dp, if (selected) color else MaterialTheme.colorScheme.outlineVariant),
                    modifier = Modifier.fillMaxWidth().clickable(enabled = enabled) { level = l.wire },
                ) {
                    Row(Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(l.title, fontWeight = FontWeight.SemiBold, color = if (enabled) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.outline)
                            Text(
                                if (enabled) l.detail else "$name hasn't allowed you to use this level",
                                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        if (!enabled) Icon(Icons.Filled.Lock, null, tint = MaterialTheme.colorScheme.outline)
                    }
                }
            }
            OutlinedTextField(
                note, { note = it.take(140) }, label = { Text("Add a short note (optional)") }, singleLine = true,
                modifier = Modifier.fillMaxWidth(), supportingText = { Text("${note.length}/140") },
            )
            Button(
                onClick = { sending = true; onSend(level, note) }, enabled = !sending,
                colors = ButtonDefaults.buttonColors(containerColor = if (level == "emergency") BuzzRed else BuzzAmber),
                modifier = Modifier.fillMaxWidth().height(54.dp),
            ) {
                Text(if (level == "emergency") "Send emergency buzz" else "Send ${levels.first { it.wire == level }.title.lowercase()}", fontWeight = FontWeight.Bold)
            }
        }
    }
}
