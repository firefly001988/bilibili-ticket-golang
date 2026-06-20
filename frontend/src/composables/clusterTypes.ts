export type ResourceRole = 'primary' | 'standby'
export type Phase = 'punctual' | 'reflow'

export interface AccountSummary {
  id: string
  name: string
  role: ResourceRole
  enabled: boolean
  cooldownUntil?: string
  credentialVersion: number
}

export interface WorkerSummary {
  id: string
  name: string
  baseUrl: string
  role: ResourceRole
  enabled: boolean
  healthy: boolean
  activeAttemptId?: string
  version?: string
}

export interface MacroSummary {
  id: string
  taskGroupId: string
  projectId: number
  screenId: number
  skuId: number
  eventDay: string
  eventDayConfirmed: boolean
  needsReview: boolean
  smartMerge: boolean
  orderCapacity: number
  priority: number
  desiredReplicas: number
  hardConcurrency: number
  phase: Phase
}

export interface AttemptSummary {
  id: string
  intentId: string
  accountId: string
  workerId: string
  state: string
  orderId?: string
  reason?: string
}

export interface ClusterSnapshot {
  taskGroups: Array<{ id: string; name: string; createdAt: string }>
  accounts: AccountSummary[]
  workers: WorkerSummary[]
  macros: MacroSummary[]
  attempts: AttemptSummary[]
}

function service(): any {
  return (window as any)?.go?.main?.ClusterService
}

export async function clusterCall<T>(method: string, ...args: any[]): Promise<T> {
  const target = service()
  if (!target?.[method]) throw new Error(`ClusterService.${method} is unavailable`)
  return target[method](...args) as Promise<T>
}
