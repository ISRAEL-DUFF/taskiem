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
        ?.mapNotNull { runCatching { String(java.util.Base64.getDecoder().decode(it)) }.getOrNull() }
        .orEmpty()
    val url = defines.firstOrNull { it.startsWith("TASKIEM_URL=") }?.substringAfter("=") ?: "https://staging.ispend.name.ng"
    runCatching { java.net.URI(url).host }.getOrNull() ?: "staging.ispend.name.ng"
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
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "app.taskiem.chat"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
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

    buildTypes {
        release {
            // TODO: Add your own signing config for the release build.
            // Signing with the debug keys for now, so `flutter run --release` works.
            signingConfig = signingConfigs.getByName("debug")
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
