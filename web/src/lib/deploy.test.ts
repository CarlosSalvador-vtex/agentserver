import { describe, it, expect, vi, beforeEach } from 'vitest'
import { deployAgent } from './deploy'
import * as api from './api'

// ApiError lives in apiClient; deploy.ts imports it from there.
// We need the real class so instanceof checks work correctly.
vi.mock('./api', async (importOriginal) => {
  const real = await importOriginal<typeof import('./api')>()
  return {
    ...real,
    listSandboxes: vi.fn(),
    deleteSandbox: vi.fn(),
    createSandbox: vi.fn(),
  }
})

const mockList = api.listSandboxes as ReturnType<typeof vi.fn>
const mockDelete = api.deleteSandbox as ReturnType<typeof vi.fn>
const mockCreate = api.createSandbox as ReturnType<typeof vi.fn>

beforeEach(() => {
  vi.clearAllMocks()
})

describe('deployAgent', () => {
  it('throws when listSandboxes fails', async () => {
    mockList.mockRejectedValue(new Error('network error'))
    await expect(deployAgent('ws1', 'soul1', [])).rejects.toThrow('Erro ao listar agentes')
  })

  it('skips delete when no existing sandboxes, creates new', async () => {
    mockList.mockResolvedValue([])
    mockCreate.mockResolvedValue({ id: 'sbx-new' })
    const result = await deployAgent('ws1', 'soul1', ['skill1'])
    expect(mockDelete).not.toHaveBeenCalled()
    expect(mockCreate).toHaveBeenCalledWith(
      'ws1', 'Agente Produção', 'openclaw',
      undefined, undefined, undefined, undefined,
      { soul: 'draft:soul1', skills: ['draft:skill1'] }
    )
    expect(result.sandboxId).toBe('sbx-new')
  })

  it('deletes existing sandbox before creating new', async () => {
    mockList.mockResolvedValue([{ id: 'sbx-old' }])
    mockDelete.mockResolvedValue(undefined)
    mockCreate.mockResolvedValue({ id: 'sbx-new' })
    const result = await deployAgent('ws1', 'soul1', [])
    expect(mockDelete).toHaveBeenCalledWith('sbx-old')
    expect(result.sandboxId).toBe('sbx-new')
  })

  it('treats 404 on delete as success (sandbox already gone)', async () => {
    const { ApiError } = await import('./apiClient')
    mockList.mockResolvedValue([{ id: 'sbx-gone' }])
    mockDelete.mockRejectedValue(new ApiError(404, 'not found'))
    mockCreate.mockResolvedValue({ id: 'sbx-new' })
    await expect(deployAgent('ws1', 'soul1', [])).resolves.toEqual({ sandboxId: 'sbx-new' })
  })

  it('throws PT-BR message on quota_exceeded', async () => {
    mockList.mockResolvedValue([])
    mockCreate.mockRejectedValue({ error: 'quota_exceeded', message: 'limit reached' })
    await expect(deployAgent('ws1', 'soul1', [])).rejects.toThrow('Workspace sem cota')
  })

  it('re-throws unknown create errors', async () => {
    mockList.mockResolvedValue([])
    mockCreate.mockRejectedValue(new Error('internal server error'))
    await expect(deployAgent('ws1', 'soul1', [])).rejects.toThrow('internal server error')
  })
})
