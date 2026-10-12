package io.github.xattribution.burrow.core

import kotlinx.coroutines.CoroutineDispatcher

/** Where blocking network and file reads run. */
internal expect val ioDispatcher: CoroutineDispatcher
