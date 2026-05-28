import { listSandboxes, deleteSandbox, createSandbox } from './api'
import { ApiError } from './apiClient'

export interface DeployResult {
  sandboxId: string
}

export async function deployAgent(
  workspaceId: string,
  soulId: string,
  skillIds: string[],
): Promise<DeployResult> {
  // 1. List and delete all existing sandboxes in the workspace
  let sandboxes: Awaited<ReturnType<typeof listSandboxes>>
  try {
    sandboxes = await listSandboxes(workspaceId)
  } catch (e) {
    throw new Error(`Erro ao listar agentes: ${e instanceof Error ? e.message : String(e)}`)
  }

  await Promise.all(
    sandboxes.map((s) =>
      deleteSandbox(s.id).catch((e) => {
        // 404 means already gone — treat as success
        if (e instanceof ApiError && e.status === 404) return
        throw e
      }),
    ),
  )

  // 2. Create new sandbox with composition
  try {
    const sandbox = await createSandbox(
      workspaceId,
      'Agente Produção',
      'openclaw',
      undefined,
      undefined,
      undefined,
      undefined,
      {
        soul: `draft:${soulId}`,
        skills: skillIds.map((id) => `draft:${id}`),
      },
    )
    return { sandboxId: sandbox.id }
  } catch (e) {
    if (e && typeof e === 'object' && 'error' in e && (e as Record<string, unknown>).error === 'quota_exceeded') {
      throw new Error('Workspace sem cota para agentes — peça ao administrador para configurar a cota.')
    }
    throw e
  }
}
