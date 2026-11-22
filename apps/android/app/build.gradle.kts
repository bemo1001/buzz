plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
}

// Optional Firebase config for push wake-ups. Without it, Buzzit keeps its own
// connection open (foreground service), so it works with no Google services.
fun prop(name: String) = (project.findProperty(name) as String?) ?: ""

android {
    namespace = "app.buzzit"
    compileSdk = 35

    defaultConfig {
        applicationId = "app.buzzit"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "1.0.0"

        buildConfigField("String", "DEFAULT_SERVER_URL", "\"${prop("buzzit.serverUrl").ifEmpty { "http://10.0.2.2:8080" }}\"")
        buildConfigField("String", "FCM_PROJECT_ID", "\"${prop("buzzit.fcm.projectId")}\"")
        buildConfigField("String", "FCM_APP_ID", "\"${prop("buzzit.fcm.appId")}\"")
        buildConfigField("String", "FCM_API_KEY", "\"${prop("buzzit.fcm.apiKey")}\"")
        buildConfigField("String", "FCM_SENDER_ID", "\"${prop("buzzit.fcm.senderId")}\"")
    }

    signingConfigs {
        create("release") {
            val ks = prop("buzzit.keystore")
            if (ks.isNotEmpty()) {
                storeFile = file(ks)
                storePassword = prop("buzzit.keystorePassword")
                keyAlias = prop("buzzit.keyAlias")
                keyPassword = prop("buzzit.keyPassword")
            }
        }
    }

    buildTypes {
        debug {
            // Allows http:// to a local dev server (e.g. the emulator's 10.0.2.2).
            manifestPlaceholders["usesCleartext"] = "true"
        }
        release {
            manifestPlaceholders["usesCleartext"] = "false"
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            if (prop("buzzit.keystore").isNotEmpty()) signingConfig = signingConfigs.getByName("release")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    testOptions { unitTests.isReturnDefaultValues = true }
}

ksp {
    arg("room.schemaLocation", "$projectDir/schemas")
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    // Firebase pulls an old fragment; ActivityResult APIs need >= 1.3.
    implementation(libs.androidx.fragment.ktx)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.process)
    implementation(libs.androidx.navigation.compose)
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons)
    debugImplementation(libs.compose.ui.tooling)

    implementation(libs.okhttp)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.room.runtime)
    implementation(libs.room.ktx)
    ksp(libs.room.compiler)
    implementation(libs.work.runtime.ktx)
    implementation(libs.coil.compose)
    implementation(libs.datastore.preferences)
    implementation(libs.security.crypto)
    implementation(libs.firebase.messaging)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
}
