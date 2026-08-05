package app.bowtie.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * TokenStore contract tests run against [InMemoryTokenStore] on the JVM.
 * EncryptedTokenStore is not Robolectric-tested (AndroidKeyStore footgun).
 */
class TokenStoreTest {

    private fun newStore(): TokenStore = InMemoryTokenStore()

    @Test
    fun initiallyEmpty() {
        val store = newStore()
        assertNull(store.loadServer())
        assertNull(store.loadRefreshToken())
    }

    @Test
    fun saveAndLoad() {
        val store = newStore()
        store.save("http://192.168.1.10:8400", "refresh-abc")
        assertEquals("http://192.168.1.10:8400", store.loadServer())
        assertEquals("refresh-abc", store.loadRefreshToken())
    }

    @Test
    fun nullClearsEachFieldIndependently() {
        val store = newStore()
        store.save("http://host", "rt")
        store.save("http://host", null)
        assertEquals("http://host", store.loadServer())
        assertNull(store.loadRefreshToken())

        store.save(null, "rt2")
        assertNull(store.loadServer())
        assertEquals("rt2", store.loadRefreshToken())

        store.save(null, null)
        assertNull(store.loadServer())
        assertNull(store.loadRefreshToken())
    }

    @Test
    fun overwriteReplaces() {
        val store = newStore()
        store.save("http://a", "r1")
        store.save("http://b", "r2")
        assertEquals("http://b", store.loadServer())
        assertEquals("r2", store.loadRefreshToken())
    }
}
