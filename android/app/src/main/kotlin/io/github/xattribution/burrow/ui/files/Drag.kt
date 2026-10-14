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
    private val onLift: () -> Unit,
) {
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
            return DragAndDropTransferData(ClipData.newPlainText(e.name, e.name), localState = e, flags = 0)
        }
        val clip = ClipData(e.name, arrayOf(RemoteFileProvider.mimeOf(e.name)), ClipData.Item(RemoteFileProvider.uriFor(ctx, e)))
        return DragAndDropTransferData(clip, localState = e, flags = View.DRAG_FLAG_GLOBAL or View.DRAG_FLAG_GLOBAL_URI_READ)
    }

    /** A drop target for [dir]; with [enabled] false it only notices the drag end. */
    fun target(dir: String, enabled: Boolean = true): DragAndDropTarget = object : DragAndDropTarget {
        override fun onEntered(event: DragAndDropEvent) {
            if (!enabled) return
            val m = moving(event)
            if (m != null && !canMove(m.path, dir)) return
            over = dir
            hint = (if (m != null) "Move to " else "Upload to ") + dir.substringAfterLast('/')
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

    private fun moving(event: DragAndDropEvent) = event.toAndroidDragEvent().localState as? Entry

    private fun drop(event: DragAndDropEvent, dir: String): Boolean {
        val m = moving(event)
        if (m != null) {
            if (!canMove(m.path, dir)) return false
            vm.act(done = "Moved ${m.name} to ${dir.substringAfterLast('/')}") { it.move(m.path, "$dir/${m.name}") }
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
                uris.forEach { u -> runCatching { ctx.graph.transfers.upload(stageForUpload(ctx, u), dir) } }
            } finally {
                perms?.release()
            }
        }
        vm.message.value = "Sending ${uris.size} file${if (uris.size == 1) "" else "s"} to ${dir.substringAfterLast('/')}"
        return true
    }

    companion object {
        /** Not into the folder it's already in, onto itself, or into its own subfolder. */
        fun canMove(src: String, dest: String) =
            dest != src.substringBeforeLast('/', "") && dest != src && !dest.startsWith("$src/") && dest.isNotEmpty()
    }
}
