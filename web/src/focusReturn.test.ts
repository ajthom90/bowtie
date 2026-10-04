import { afterEach, describe, expect, it } from 'vitest'
import { findFocusTarget, focusMarkOf } from './focusReturn'

function setBody(html: string) {
  document.body.innerHTML = html
}

describe('focusMarkOf', () => {
  afterEach(() => setBody(''))

  it('remembers a control by its aria-label, else its text', () => {
    setBody(
      '<button id="a" aria-label="Watch channel 9.1 FOX 9">9.1</button><button id="b"> Play </button>',
    )
    expect(focusMarkOf(document.getElementById('a'))).toEqual({
      label: 'Watch channel 9.1 FOX 9',
      text: '9.1',
    })
    expect(focusMarkOf(document.getElementById('b'))).toEqual({ label: null, text: 'Play' })
  })

  it('remembers nothing for the page body or no element', () => {
    expect(focusMarkOf(document.body)).toBeNull()
    expect(focusMarkOf(null)).toBeNull()
  })
})

describe('findFocusTarget', () => {
  afterEach(() => setBody(''))

  it('finds the same control again by aria-label', () => {
    setBody(
      '<input aria-label="Search the guide"><button aria-label="Watch channel 5.1 KSTP">5.1</button>' +
        '<button aria-label="Watch channel 9.1 FOX 9">9.1</button>',
    )
    const el = findFocusTarget(document, { label: 'Watch channel 9.1 FOX 9', text: '9.1' })
    expect(el?.getAttribute('aria-label')).toBe('Watch channel 9.1 FOX 9')
  })

  it('handles quotes in labels', () => {
    setBody(`<button aria-label='Say "hi", channel 2'>x</button>`)
    expect(findFocusTarget(document, { label: 'Say "hi", channel 2', text: 'x' })).not.toBeNull()
  })

  it('falls back to a button with the same text', () => {
    setBody('<button>Guide</button><button>Play</button>')
    expect(findFocusTarget(document, { label: null, text: 'Play' })?.textContent).toBe('Play')
  })

  it('without a match, picks the first focusable control (skipping disabled and hidden ones)', () => {
    setBody(
      '<p>Bowtie</p><button disabled>Off</button><button tabindex="-1">Skip</button>' +
        '<input aria-label="Search the guide"><button>Prev</button>',
    )
    expect(findFocusTarget(document, { label: 'Gone', text: 'Gone' })?.getAttribute('aria-label')).toBe(
      'Search the guide',
    )
    expect(findFocusTarget(document, null)?.getAttribute('aria-label')).toBe('Search the guide')
  })

  it('exact only: no fallback when asked not to', () => {
    setBody('<button>Prev</button>')
    expect(findFocusTarget(document, { label: 'Gone', text: null }, { fallback: false })).toBeNull()
  })
})
