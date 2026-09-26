import java.io.File
import java.util.Base64

plugins {
    id("com.android.application")
    id("com.google.gms.google-services")
    id("io.sentry.android.gradle") version "6.14.0"
    id("org.jetbrains.kotlin.plugin.compose")
}

val gitHash = providers.exec {
    commandLine("git", "rev-parse", "--short", "HEAD")
}.standardOutput.asText.map { it.trim() }

// Network-security XML is generated so a build can optionally bundle a private
// CA without committing the certificate. Templates live in network-security/.
val generatedNetworkSecurityDir = layout.buildDirectory.dir("generated/res/networkSecurity")
val networkSecurityTemplates = layout.projectDirectory.dir("network-security")

val generateNetworkSecurityConfig = tasks.register("generateNetworkSecurityConfig") {
    inputs.dir(networkSecurityTemplates)
    val caFileEnv = providers.environmentVariable("ANDROID_PRIVATE_CA_FILE").orElse("")
    val caB64Env = providers.environmentVariable("ANDROID_PRIVATE_CA_BASE64").orElse("")
    inputs.property("privateCaFile", caFileEnv)
    // Track presence only so the certificate bytes are not stored in the build cache key as a loggable property name collision.
    inputs.property("privateCaProvided", caB64Env.map { if (it.isBlank()) "absent" else "present" })
    outputs.dir(generatedNetworkSecurityDir)

    doLast {
        val output = generatedNetworkSecurityDir.get().asFile
        val xmlDir = output.resolve("xml")
        val rawDir = output.resolve("raw")
        xmlDir.mkdirs()

        val certBytes = readOptionalPrivateCa(caFileEnv.get(), caB64Env.get(), project.projectDir)
        val templateName = if (certBytes == null) {
            "network_security_config.xml"
        } else {
            rawDir.mkdirs()
            rawDir.resolve("private_ca.crt").writeBytes(certBytes)
            "network_security_config.with_ca.xml"
        }
        val template = networkSecurityTemplates.file(templateName).asFile
        xmlDir.resolve("network_security_config.xml").writeText(template.readText())
    }
}

android.sourceSets.named("main") {
    res.directories.add(generatedNetworkSecurityDir.get().asFile.path)
}

tasks.named("preBuild").configure {
    dependsOn(generateNetworkSecurityConfig)
}

android {
    compileSdk = 37

    defaultConfig {
        applicationId = "com.httpsms"
        minSdk = 28
        targetSdk = 37
        versionCode = 1
        versionName = gitHash.getOrElse("unknown")
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    val releaseKeystoreFile = providers.environmentVariable("ANDROID_KEYSTORE_FILE").orNull
    val releaseKeystorePassword = providers.environmentVariable("ANDROID_KEYSTORE_PASSWORD").orNull
    val releaseKeyAlias = providers.environmentVariable("ANDROID_KEY_ALIAS").orNull
    val releaseKeyPassword = providers.environmentVariable("ANDROID_KEY_PASSWORD").orNull
    val hasReleaseKeystore = !releaseKeystoreFile.isNullOrBlank() &&
        !releaseKeystorePassword.isNullOrBlank() &&
        !releaseKeyAlias.isNullOrBlank() &&
        !releaseKeyPassword.isNullOrBlank() &&
        project.file(releaseKeystoreFile).isFile

    signingConfigs {
        if (hasReleaseKeystore) {
            create("release") {
                storeFile = project.file(releaseKeystoreFile!!)
                storePassword = releaseKeystorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
            }
        }
    }

    buildTypes {
        getByName("debug") {
            manifestPlaceholders["sentryEnvironment"] = "development"
        }
        getByName("release") {
            manifestPlaceholders["sentryEnvironment"] = "production"
            isMinifyEnabled = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            // Release keystore secrets are optional. Without them the APK is still a
            // release build, signed with the debug key so it can be installed for LAN testing.
            signingConfig = if (hasReleaseKeystore) {
                signingConfigs.getByName("release")
            } else {
                signingConfigs.getByName("debug")
            }
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_1_8
        targetCompatibility = JavaVersion.VERSION_1_8
    }
    namespace = "com.httpsms"

    buildFeatures {
        buildConfig = true
        compose = true
    }
}

dependencies {
    val composeBom = platform("androidx.compose:compose-bom:2026.06.01")
    implementation(composeBom)
    androidTestImplementation(composeBom)

    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.activity:activity-compose:1.13.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.11.0")

    implementation(platform("com.google.firebase:firebase-bom:34.16.0"))
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    implementation("com.google.firebase:firebase-analytics")
    implementation("com.google.firebase:firebase-messaging")
    implementation("com.squareup.okhttp3:okhttp:5.4.0")
    implementation("com.jakewharton.timber:timber:5.0.1")
    implementation("androidx.preference:preference-ktx:1.2.1")
    implementation("androidx.work:work-runtime-ktx:2.11.2")
    implementation("androidx.core:core-ktx:1.19.0")
    implementation("androidx.cardview:cardview:1.0.0")
    implementation("com.beust:klaxon:5.6")
    implementation("androidx.appcompat:appcompat:1.7.1")
    implementation("org.apache.commons:commons-text:1.15.0")
    implementation("com.google.android.material:material:1.14.0")
    implementation("androidx.constraintlayout:constraintlayout:2.2.1")
    implementation("com.googlecode.libphonenumber:libphonenumber:9.0.34")
    implementation("com.klinkerapps:android-smsmms:5.2.6")
    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.7.0")
}

private fun readOptionalPrivateCa(caFile: String, caBase64: String, projectDir: File): ByteArray? {
    if (caBase64.isNotBlank()) {
        return try {
            Base64.getDecoder().decode(caBase64.trim())
        } catch (error: IllegalArgumentException) {
            throw org.gradle.api.GradleException("ANDROID_PRIVATE_CA_BASE64 is not valid base64", error)
        }
    }
    if (caFile.isBlank()) {
        return null
    }
    val candidate = File(caFile)
    val resolved = if (candidate.isAbsolute) candidate else File(projectDir, caFile)
    if (!resolved.isFile) {
        throw org.gradle.api.GradleException("ANDROID_PRIVATE_CA_FILE does not exist: $caFile")
    }
    return resolved.readBytes()
}
