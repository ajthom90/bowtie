package app.bowtie.tv.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ContinueFocusTest {

    @Test
    fun removingACard_focusesTheNextOne() {
        assertEquals(3L, ContinueFocus.afterRemoval(listOf(1L, 2L, 3L), removedId = 2L))
        assertEquals(2L, ContinueFocus.afterRemoval(listOf(1L, 2L, 3L), removedId = 1L))
    }

    @Test
    fun removingTheLastCard_focusesThePreviousOne() {
        assertEquals(2L, ContinueFocus.afterRemoval(listOf(1L, 2L, 3L), removedId = 3L))
    }

    @Test
    fun removingTheOnlyCard_leavesTheRow() {
        // The row disappears; the screen moves focus below it.
        assertNull(ContinueFocus.afterRemoval(listOf(5L), removedId = 5L))
    }

    @Test
    fun unknownCard_hasNoNeighbor() {
        assertNull(ContinueFocus.afterRemoval(listOf(1L, 2L), removedId = 9L))
    }
}
