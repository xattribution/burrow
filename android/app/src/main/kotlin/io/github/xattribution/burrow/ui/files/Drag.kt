package io.github.xattribution.burrow.ui.files

import android.app.Activity
import android.content.ClipData
import android.content.Context
import android.view.View
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.draganddrop.DragAndDropEvent
import androidx.compose.ui.draganddrop.DragAndDropTarget
import androidx.compose.ui.draganddrop.DragAndDropTransferData
import androidx.compose.ui.draganddrop.toAndroidDragEvent
import io.github.xattribution.burrow.core.Entry
import io.github.xattribution.burrow.data.RemoteFileProvider
import io.github.xattribution.burrow.graph
import io.github.xattribution.burrow.work.stageForUpload
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

/**
 * Drag and drop in the file list. Hold a file or folder and drag it onto a folder
 * or the path bar to move it there. Drag a file out to an app beside Burrow (split
 * screen, DeX, a foldable's other half) and that app reads it straight from the
 * NAS. Files dragged in from another app are uploaded to the folder they land on.
 */
class DragDrop(
    private val ctx: Context,
    private val scope: CoroutineScope,
    private val vm: FilesViewModel,
    /** The place this list shows: drops from another place are copied, not moved. */
    val place: String,
    private val onLift: () -> Unit,
) {
    /** What a drag inside the app carries, so the list it lands on knows where it came from. */
    data class Dragged(val place: String, val entry: Entry)

    val isBurrow = vm.fs.isBurrow
    private val rootWritable = !isBurrow // an SMB share's top is a folder; a Burrow's is the list of libraries

    /** The folder under the drag, which lights up, and what dropping there does. */
    var over by mutableStateOf<String?>(null)
        private set
    var hint by mutableStateOf<String?>(null)
        private set
    /** The item being dragged, dimmed in the list. */
    var lifted by mutableStateOf<String?>(null)
        private set

    fun transfer(e: Entry): DragAndDropTransferData {
        lifted = e.path
        onLift()
        if (e.dir) {
            // folders only move inside Burrow, so the drag stays in this app
            return DragAndDropTransferData(ClipData.newPlainText(e.name, e.name), localState = Dragged(place, e), flags = 0)
        }
        val clip = ClipData(e.name, arrayOf(RemoteFileProvider.mimeOf(e.name)), ClipData.Item(RemoteFileProvider.uriFor(ctx, e, place)))
        return DragAndDropTransferData(clip, localState = Dragged(place, e), flags = View.DRAG_FLAG_GLOBAL or View.DRAG_FLAG_GLOBAL_URI_READ)
    }

    /** A drop target for [dir]; with [enabled] false it only notices the drag end. */
    fun target(dir: String, enabled: Boolean = true): DragAndDropTarget = object : DragAndDropTarget {
        override fun onEntered(event: DragAndDropEvent) {
            if (!enabled) return
            val m = moving(event)
            if (m != null && m.place == place && !canMove(m.entry.path, dir, rootWritable)) return
            over = dir
            val verb = when {
                m == null -> "Upload to "
                m.place == place -> "Move to "
                else -> "Copy to "
            }
            hint = verb + dir.substringAfterLast('/').ifEmpty { ctx.graph.places.get(place)?.name ?: "Burrow" }
        }

        override fun onExited(event: DragAndDropEvent) {
            if (over == dir) clear()
        }

        override fun onEnded(event: DragAndDropEvent) {
            clear()
            lifted = null
        }

        override fun onDrop(event: DragAndDropEvent): Boolean {
            clear()
            return enabled && drop(event, dir)
        }
    }

    private fun clear() {
        over = null
        hint = null
    }

    private fun moving(event: DragAndDropEvent) = event.toAndroidDragEvent().localState as? Dragged

    private fun drop(event: DragAndDropEvent, dir: String): Boolean {
        val m = moving(event)
        if (m != null) {
            val e = m.entry
            val where = dir.substringAfterLast('/').ifEmpty { ctx.graph.places.get(place)?.name ?: "Burrow" }
            if (m.place != place) {
                // between places: a copy through the phone, the original stays
                ctx.graph.transfers.copy(m.place, e, place, dir, move = false)
                vm.message.value = "Copying ${e.name} to $where"
                return true
            }
            if (!canMove(e.path, dir, rootWritable)) return false
            vm.act(done = "Moved ${e.name} to $where") { it.move(e.path, if (dir.isEmpty()) e.name else "$dir/${e.name}") }
            return true
        }
        val de = event.toAndroidDragEvent()
        val clip = de.clipData ?: return false
        val uris = (0 until clip.itemCount).mapNotNull { clip.getItemAt(it).uri }
        if (uris.isEmpty()) return false
        val perms = (ctx as? Activity)?.requestDragAndDropPermissions(de)
        // copied off the main thread, while the grant from the other app still holds
        scope.launch(Dispatchers.IO) {
            try {
                uris.forEach { u -> runCatching { ctx.graph.transfers.upload(stageForUpload(ctx, u), dir, place = place) } }
            } finally {
                perms?.release()
            }
        }
        vm.message.value = "Sending ${uris.size} file${if (uris.size == 1) "" else "s"} to ${dir.substringAfterLast('/')}"
        return true
    }

    companion object {
        /** Not into the folder it's already in, onto itself, or into its own subfolder. */
        fun canMove(src: String, dest: String, rootWritable: Boolean = false) =
            dest != src.substringBeforeLast('/', "") && dest != src && !dest.startsWith("$src/") && (dest.isNotEmpty() || rootWritable)
    }
}
