import axios from 'axios'
import { describe, expect, it, vi } from 'vitest'

// We mock axios.create so the api module's interceptor wiring stays
// happy, but we let the individual HTTP methods fall through to the
// mocked http instance passed into the module via a single
// dependency-injected factory. Concretely: since client.ts builds its
// own axios.create call, we stub the entire axios module with a
// minimal fake whose create() returns a controllable stub.
const stubHttp = {
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  put: vi.fn(),
  delete: vi.fn(),
  interceptors: {
    response: { use: vi.fn() },
  },
}

vi.mock('axios', () => ({
  default: {
    create: vi.fn(() => stubHttp),
  },
}))

// Import AFTER the mock so client.ts picks up the stub.
const { api } = await import('@/shared/api/client')

describe('api client', () => {
  it('listEvents falls back to [] when the server returns null', async () => {
    stubHttp.get.mockResolvedValue({ data: { events: null } })
    const events = await api.listEvents({ from: '2026-01-01T00:00:00Z', to: '2026-01-02T00:00:00Z' })
    expect(events).toEqual([])
  })

  it('listEvents returns server-provided rows when present', async () => {
    const sample = [
      { id: 'a', title: 'Stand-up', start_at: '', end_at: '', all_day: false, created_at: '', updated_at: '' },
    ]
    stubHttp.get.mockResolvedValue({ data: { events: sample } })
    const events = await api.listEvents({ from: 'x', to: 'y' })
    expect(events).toBe(sample)
  })

  it('getTimeReport forwards from/to query params and returns the body', async () => {
    const report = { agent_id: 'u-1', from: 'x', to: 'y', tasks: [], total_sec: 0 }
    stubHttp.get.mockResolvedValue({ data: report })
    const out = await api.getTimeReport({
      from: '2026-01-01T00:00:00Z',
      to: '2026-01-02T00:00:00Z',
    })
    expect(out).toBe(report)
    expect(stubHttp.get).toHaveBeenCalledWith('/api/v1/reports/time', {
      params: { from: '2026-01-01T00:00:00Z', to: '2026-01-02T00:00:00Z' },
    })
  })

  it('updateColumn sends a PATCH with the body verbatim', async () => {
    stubHttp.patch.mockResolvedValue({
      data: { id: 'col-1', board_id: 'b', name: 'New', position: 1, wip_limit: 3 },
    })
    await api.updateColumn('col-1', { name: 'New', wip_limit: 3, color: '#fff' })
    expect(stubHttp.patch).toHaveBeenCalledWith('/api/v1/columns/col-1', {
      name: 'New',
      wip_limit: 3,
      color: '#fff',
    })
  })

  it('createColumn POSTs the project-scoped endpoint and returns the column', async () => {
    const created = { id: 'col-2', board_id: 'b', name: 'QA', position: 6144, wip_limit: null }
    stubHttp.post.mockResolvedValue({ data: created })
    const out = await api.createColumn('p-1', { name: 'QA' })
    expect(out).toBe(created)
    expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/projects/p-1/columns', { name: 'QA' })
  })

  it('deleteColumn DELETEs the column endpoint and resolves to void', async () => {
    stubHttp.delete.mockResolvedValue({ data: undefined })
    await api.deleteColumn('col-2')
    expect(stubHttp.delete).toHaveBeenCalledWith('/api/v1/columns/col-2')
  })

  it('deletePage returns void and hits the right URL', async () => {
    stubHttp.delete.mockResolvedValue({ data: undefined })
    await api.deletePage('to-delete')
    expect(stubHttp.delete).toHaveBeenCalledWith('/api/v1/pages/to-delete')
  })

  // Reference axios so the import isn't pruned by tree-shaking in
  // test mode.
  it('axios.create was called once at module load', () => {
    expect(vi.mocked(axios.create)).toHaveBeenCalled()
  })
})