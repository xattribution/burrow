package io.github.xattribution.burrow.ui.files

import androidx.lifecycle.ViewModel
import io.github.xattribution.burrow.data.Connection
import androidx.lifecycle.viewModelScope
import io.github.xattribution.burrow.core.Entry
import io.github.xattribution.burrow.core.Listing
import io.github.xattribution.burrow.core.SearchResult
import io.github.xattribution.burrow.ui.common.Load
import io.github.xattribution.burrow.ui.common.friendly
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

class FilesViewModel(private val conn: Connection) : ViewModel() {

    private val _path = MutableStateFlow("")
    val path: StateFlow<String> = _path
    private val _listing = MutableStateFlow<Load<Listing>>(Load.Loading)
    val listing: StateFlow<Load<Listing>> = _listing
    private val _query = MutableStateFlow("")
    val query: StateFlow<String> = _query
    private val _results = MutableStateFlow<Load<SearchResult>?>(null)
    val results: StateFlow<Load<SearchResult>?> = _results
    private val _favorites = MutableStateFlow<List<Entry>>(emptyList())
    val favorites: StateFlow<List<Entry>> = _favorites
    val grid = MutableStateFlow(false)
    val message = MutableStateFlow<String?>(null)

    private var loadJob: Job? = null
    private var searchJob: Job? = null

    init {
        open("")
    }

    fun open(p: String) {
        _path.value = p
        refresh()
    }

    fun up(): Boolean {
        val p = _path.value
        if (p.isEmpty()) return false
        open(p.substringBeforeLast('/', ""))
        return true
    }

    fun refresh() {
        val p = _path.value
        loadJob?.cancel()
        _listing.value = Load.Loading
        loadJob = viewModelScope.launch {
            _listing.value = try {
                val l = conn.use { it.ls(p) }
                if (p.isEmpty()) _favorites.value = runCatching { conn.use { it.favorites() } }.getOrDefault(emptyList())
                // folders of mostly photos open as a grid
                val media = l.entries.count { it.kind == "image" || it.kind == "video" }
                if (l.entries.isNotEmpty() && media * 2 > l.entries.size) grid.value = true
                else if (media == 0) grid.value = false
                Load.Ready(l)
            } catch (e: Exception) {
                if (e is kotlinx.coroutines.CancellationException) throw e
                Load.Failed(friendly(e))
            }
        }
    }

    fun search(q: String) {
        _query.value = q
        searchJob?.cancel()
        if (q.isBlank()) {
            _results.value = null
            return
        }
        searchJob = viewModelScope.launch {
            delay(250) // wait for typing to pause
            _results.value = Load.Loading
            _results.value = try {
                Load.Ready(conn.use { it.search(q.trim(), root = _path.value.ifEmpty { null }) })
            } catch (e: Exception) {
                if (e is kotlinx.coroutines.CancellationException) throw e
                Load.Failed(friendly(e))
            }
        }
    }

    /** Runs an action, then reloads the folder; failures become a message. */
    fun act(done: String? = null, block: suspend (io.github.xattribution.burrow.core.BurrowApi) -> Unit) {
        viewModelScope.launch {
            try {
                conn.use { block(it) }
                if (done != null) message.value = done
                refresh()
                if (_query.value.isNotBlank()) search(_query.value)
            } catch (e: Exception) {
                if (e is kotlinx.coroutines.CancellationException) throw e
                message.value = friendly(e)
            }
        }
    }
}
