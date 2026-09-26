package com.httpsms

import java.net.URI

/**
 * Validates the operator-entered API base URL.
 * The URL is stored on the device at runtime and is never baked into the build.
 */
object ServerUrls {
    fun normalize(input: String): String? {
        val trimmed = input.trim().trimEnd('/')
        if (trimmed.isEmpty()) {
            return null
        }
        val uri = try {
            URI(trimmed)
        } catch (_: Exception) {
            return null
        }
        if (!uri.isAbsolute || !uri.scheme.equals("https", ignoreCase = true)) {
            return null
        }
        if (!uri.userInfo.isNullOrEmpty()) {
            return null
        }
        val host = uri.host ?: return null
        if (host.isBlank()) {
            return null
        }
        if (!host.equals("localhost", ignoreCase = true) && !host.contains('.')) {
            return null
        }
        if (!uri.rawQuery.isNullOrEmpty() || !uri.rawFragment.isNullOrEmpty()) {
            return null
        }
        val path = uri.path ?: ""
        if (path.isNotEmpty() && path != "/") {
            return null
        }
        return trimmed
    }
}
