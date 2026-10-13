package app.bowtie.tv.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LoginModeTest {
    // A phone that set the TV up waits for a sign-in code, so Login must ask for one by itself.
    @Test fun opensOnPhoneSignInWhenTheServerIsKnown() = assertTrue(opensInPhoneMode(deviceSignInAvailable = true))

    @Test fun opensOnThePasswordFormWithoutAClient() = assertFalse(opensInPhoneMode(deviceSignInAvailable = false))
}
