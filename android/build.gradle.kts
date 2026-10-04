plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.android.library) apply false
    alias(libs.plugins.kotlin.android) apply false
    alias(libs.plugins.compose.compiler) apply false
    alias(libs.plugins.kotlin.serialization) apply false
}

// Release builds take their version from the tag (release.yml sets
// BOWTIE_VERSION, e.g. "0.8.1"). versionCode must grow with every release so a
// sideloaded APK installs over the previous one: major*1_000_000 +
// minor*1_000 + patch. Local builds without it are "0.0.0-dev" / 1.
val bowtieVersion: String? = System.getenv("BOWTIE_VERSION")
    ?.removePrefix("v")
    ?.takeIf { Regex("""\d+\.\d+\.\d+""").matches(it) }
extra["bowtieVersionName"] = bowtieVersion ?: "0.0.0-dev"
extra["bowtieVersionCode"] = bowtieVersion
    ?.split(".")
    ?.map(String::toInt)
    ?.let { (major, minor, patch) -> major * 1_000_000 + minor * 1_000 + patch }
    ?: 1
