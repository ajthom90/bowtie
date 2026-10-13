import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { EditRuleDialog } from './EditRuleDialog'
import { Recordings } from './Recordings'
import type { RecordingRule } from '../api/client'

const client = vi.hoisted(() => ({
  listRecordingRules: vi.fn(),
  listRecordings: vi.fn(),
  patchRecordingRule: vi.fn(),
}))
vi.mock('../auth/AuthContext', () => ({ useAuth: () => ({ client, user: {}, logout: vi.fn() }) }))
;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const rule: RecordingRule = {
  id: 4, title: 'Drama', seriesId: 'SH1', channelId: 3, channelName: '5.1 TV',
  newOnly: true, keepLatest: 5, scheduledBy: 'alice', canManage: true, editable: true, createdAt: '',
}
let host: HTMLDivElement
let root: Root
beforeEach(() => {
  vi.resetAllMocks()
  HTMLDialogElement.prototype.showModal = vi.fn()
  client.listRecordings.mockResolvedValue([])
  client.listRecordingRules.mockResolvedValue([rule])
  client.patchRecordingRule.mockResolvedValue(rule)
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(() => { act(() => root.unmount()); host.remove() })

async function select(id: string, value: string) {
  await act(async () => {
    const field = host.querySelector<HTMLSelectElement>(`#${id}`)!
    field.value = value
    field.dispatchEvent(new Event('change', { bubbles: true }))
  })
}
async function submit() {
  await act(async () => { host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
}

it('initializes settings, saves explicit false/zero, and refreshes upcoming before rules', async () => {
  const saved = vi.fn()
  await act(async () => root.render(<EditRuleDialog rule={rule} onClose={vi.fn()} onSaved={saved} />))
  expect(host.querySelector<HTMLSelectElement>('#rule-channel')!.value).toBe('3')
  expect(host.querySelector<HTMLInputElement>('#rule-count')!.value).toBe('5')
  await select('rule-episodes', 'false')
  await select('rule-channel', '0')
  await select('rule-keep', 'true')
  await submit()
  expect(client.patchRecordingRule).toHaveBeenCalledWith(4, { newOnly: false, channelId: 0, keepLatest: 0 })
  expect(client.listRecordings).toHaveBeenLastCalledWith('upcoming')
  expect(saved).toHaveBeenCalledOnce()
  expect(client.listRecordings.mock.invocationCallOrder.at(-1)).toBeLessThan(saved.mock.invocationCallOrder[0])
})

it('shows save errors and leaves the form open', async () => {
  client.patchRecordingRule.mockRejectedValue(new Error('offline'))
  const saved = vi.fn()
  await act(async () => root.render(<EditRuleDialog rule={rule} onClose={vi.fn()} onSaved={saved} />))
  await submit()
  expect(host.querySelector('[role="alert"]')).not.toBeNull()
  expect(saved).not.toHaveBeenCalled()
  expect(host.querySelector('form')).not.toBeNull()
})

it.each([undefined, false, true])('shows Edit only with editable=true (%s)', async (editable) => {
  client.listRecordingRules.mockResolvedValue([{ ...rule, editable }])
  await act(async () => root.render(<Recordings tab="shows" onTab={vi.fn()} onGuide={vi.fn()} onPlay={vi.fn()} />))
  expect([...host.querySelectorAll('button')].some((b) => b.textContent === 'Edit')).toBe(editable === true)
})

it('hides Edit for another owner', async () => {
  client.listRecordingRules.mockResolvedValue([{ ...rule, canManage: false }])
  await act(async () => root.render(<Recordings tab="shows" onTab={vi.fn()} onGuide={vi.fn()} onPlay={vi.fn()} />))
  expect([...host.querySelectorAll('button')].some((b) => b.textContent === 'Edit')).toBe(false)
})
