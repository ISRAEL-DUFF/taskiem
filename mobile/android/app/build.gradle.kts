import java.io.FileInputStream
import java.net.URI
import java.util.Base64
import java.util.Properties

plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// The server's host, from --dart-define=TASKIEM_URL (lib/app/config.dart's
// default otherwise): the links to it that open the app (AndroidManifest.xml).
val taskiemHost: String = run {
    val defines = (project.findProperty("dart-defines") as String?)
        ?.split(",")
        ?.mapNotNull { d -> runCatching { String(Base64.getDecoder().decode(d)) }.getOrNull() }
        .orEmpty()
    val url = defines.firstOrNull { it.startsWith("TASKIEM_URL=") }?.substringAfter("=") ?: "https://staging.ispend.name.ng"
    runCatching { URI(url).host }.getOrNull() ?: "staging.ispend.name.ng"
}

// The Play Store upload key: android/key.properties (never committed; see
// mobile/README.md, "Releasing to Google Play"), or the same four values
// from the environment in CI (TASKIEM_UPLOAD_STORE_FILE, _STORE_PASSWORD,
// _KEY_ALIAS, _KEY_PASSWORD). Without it a release build is signed with
// the debug key, for trying on a phone only: Play refuses it.
val upload: Map<String, String>? = run {
    val file = rootProject.file("key.properties")
    val p = Properties()
    if (file.exists()) FileInputStream(file).use { p.load(it) }
    fun v(prop: String, env: String) = p.getProperty(prop) ?: System.getenv(env)
    val store = v("storeFile", "TASKIEM_UPLOAD_STORE_FILE")
    val storePassword = v("storePassword", "TASKIEM_UPLOAD_STORE_PASSWORD")
    val alias = v("keyAlias", "TASKIEM_UPLOAD_KEY_ALIAS")
    val keyPassword = v("keyPassword", "TASKIEM_UPLOAD_KEY_PASSWORD")
    if (store.isNullOrBlank() || storePassword.isNullOrBlank() || alias.isNullOrBlank() || keyPassword.isNullOrBlank()) null
    else mapOf("store" to store, "storePassword" to storePassword, "alias" to alias, "keyPassword" to keyPassword)
}

android {
    namespace = "app.taskiem.taskiem"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        // The app's id on Google Play: permanent once published.
        applicationId = "app.taskiem.chat"
        minSdk = 26
        targetSdk = flutter.targetSdkVersion
        // Uses the version code from pubspec.yaml. When using split APKs, 1000 * ABI_VERSION
        // is added automatically by Flutter. (https://developer.android.com/studio/build/configure-apk-splits#configure-APK-versions)
        // You can force using the value of versionCode by specifying the `-P force-version-code-ignoring-abi=true`
        // flag during build.
        versionCode = flutter.versionCode
        versionName = flutter.versionName
        manifestPlaceholders["taskiemHost"] = taskiemHost
    }

    signingConfigs {
        if (upload != null) {
            create("upload") {
                storeFile = rootProject.file(upload.getValue("store"))
                storePassword = upload.getValue("storePassword")
                keyAlias = upload.getValue("alias")
                keyPassword = upload.getValue("keyPassword")
            }
        }
    }

    buildTypes {
        release {
            signingConfig = if (upload != null) {
                signingConfigs.getByName("upload")
            } else {
                logger.warn("Taskiem: no upload key (android/key.properties): this release build is signed with the debug key and Play will refuse it.")
                signingConfigs.getByName("debug")
            }
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}

dependencies {
    // Firebase started when the phone starts the app for a notification
    // (TaskiemApplication.kt): the version firebase_core's own (its
    // FirebaseSDKVersion), so there is one Firebase in the app.
    implementation(platform("com.google.firebase:firebase-bom:35.1.0"))
    implementation("com.google.firebase:firebase-common")
}
