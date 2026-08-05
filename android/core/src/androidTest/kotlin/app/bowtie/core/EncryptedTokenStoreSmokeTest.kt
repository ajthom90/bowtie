package app.bowtie.core

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Local-only instrumentation smoke for [EncryptedTokenStore].
 * Not run in CI (AndroidKeyStore required; unit-test contract covers [InMemoryTokenStore]).
 */
@RunWith(AndroidJUnit4::class)
class EncryptedTokenStoreSmokeTest {

    @Test
    fun saveLoadClear_roundTrip() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val store = EncryptedTokenStore(context)
        store.save(null, null)

        store.save("http://10.0.0.5:8400", "refresh-smoke")
        assertEquals("http://10.0.0.5:8400", store.loadServer())
        assertEquals("refresh-smoke", store.loadRefreshToken())

        store.save(store.loadServer(), null)
        assertEquals("http://10.0.0.5:8400", store.loadServer())
        assertNull(store.loadRefreshToken())

        store.save(null, null)
        assertNull(store.loadServer())
        assertNull(store.loadRefreshToken())
    }
}
