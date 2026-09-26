package com.httpsms

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ServerUrlsTest {
    @Test
    fun acceptsHttpsOriginAndStripsTrailingSlash() {
        assertEquals(
            "https://sms.example.com",
            ServerUrls.normalize("  https://sms.example.com/  ")
        )
    }

    @Test
    fun acceptsLocalhostAndCustomPort() {
        assertEquals("https://localhost:8443", ServerUrls.normalize("https://localhost:8443"))
    }

    @Test
    fun rejectsHttpPrivateHostsAndPaths() {
        assertNull(ServerUrls.normalize("http://sms.example.com"))
        assertNull(ServerUrls.normalize("https://sms"))
        assertNull(ServerUrls.normalize("https://user:secret@sms.example.com"))
        assertNull(ServerUrls.normalize("https://sms.example.com/api"))
        assertNull(ServerUrls.normalize("not a url"))
    }
}
