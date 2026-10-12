// The shared core: the end-to-end encrypted channel to Burrow, the API client and
// the sync engine. Kotlin Multiplatform, so an iPhone app can reuse it later by
// adding iOS targets with their own Crypto and Transport (CryptoKit, URLSession).
plugins {
    alias(libs.plugins.kotlin.multiplatform)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.sqldelight)
}

kotlin {
    jvmToolchain(21)
    jvm {
        compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17) }
    }
    sourceSets {
        commonMain.dependencies {
            api(libs.coroutines.core)
            api(libs.serialization.json)
            api(libs.okio)
            api(libs.sqldelight.runtime)
            implementation(libs.sqldelight.coroutines)
        }
        jvmMain.dependencies {
            api(libs.okhttp)
            implementation(libs.bouncycastle)
        }
        jvmTest.dependencies {
            implementation(kotlin("test-junit"))
            implementation(libs.coroutines.test)
            implementation(libs.sqldelight.jvm)
        }
    }
}

sqldelight {
    databases {
        create("BurrowDb") {
            packageName.set("io.github.xattribution.burrow.core.db")
        }
    }
}

// The interop tests start a real Burrow server built from the Go code in the repo root.
tasks.withType<Test>().configureEach {
    systemProperty("burrow.repo", rootProject.projectDir.parentFile.absolutePath)
    testLogging { events("failed"); exceptionFormat = org.gradle.api.tasks.testing.logging.TestExceptionFormat.FULL }
}
