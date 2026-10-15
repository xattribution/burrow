package io.github.xattribution.burrow

import androidx.work.Configuration
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import io.github.xattribution.burrow.data.KeyWrapper

/** The app under Robolectric: a test WorkManager, and no Android Keystore (Robolectric has none). */
class TestApp : BurrowApp() {
    override fun onCreate() {
        WorkManagerTestInitHelper.initializeTestWorkManager(this, Configuration.Builder().setExecutor(SynchronousExecutor()).build())
        super.onCreate()
    }

    /** Robolectric can't answer the All files access check; tests flip this instead. */
    var allFiles = false

    override fun phoneAccess() = allFiles

    override fun keyWrapper(alias: String) = object : KeyWrapper {
        override fun wrap(plain: ByteArray) = plain.reversedArray()
        override fun unwrap(sealed: ByteArray) = sealed.reversedArray()
    }
}
