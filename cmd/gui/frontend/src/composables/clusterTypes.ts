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
  address: string
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
  projectName?: string
  screenId: number
  screenName?: string
  skuId: number
  skuName?: string
  eventDay: string
  eventDayConfirmed: boolean
  needsReview: boolean
  smartMerge: boolean
  orderCapacity: number
  capacitySource?: string
  priority: number
  desiredReplicas: number
  hardConcurrency: number
  startAt: string
  deadline: string
  phase: Phase
  purchaseGroups: PurchaseGroup[]
}

export interface PurchaseGroup {
  id: string
  macroTaskId: string
  buyers: LogicalBuyer[]
  allowSplit: boolean
  createdAt: string
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

export interface WorkerLogEntry {
  sequence: number
  time: string
  stage: string
  message: string
  code?: number
  retryable?: boolean
}

export interface BuyerAccountBadge {
  accountId: string
  accountName: string
  uid: string
}

export interface LogicalBuyer {
  logicalId: string
  name: string
  tel?: string
  idCard?: string
  type: number
  accounts?: BuyerAccountBadge[]
}

export interface CatalogSKU {
  screenId: number
  skuId: number
  screenName: string
  skuName: string
  price: number
  status?: string
  eventTime?: string
  saleStart?: string
  saleEnd?: string
  orderCapacity: number
}

export interface ProjectCatalog {
  id: string
  name: string
  forceRealName: boolean
  idBind: number
  start?: string
  end?: string
  tickets: CatalogSKU[]
}

export interface GenerateRemoteWorkerConfigResponse {
  encodedConfig: string
  workerId: string
  listen: string
}

export interface WorkerConfigResponse {
  id: string
  name: string
  address: string
  role: ResourceRole
  caCert: string
  clientCert: string
  clientKey: string
  tlsServerName: string
}

export interface ClusterSnapshot {
  taskGroups: Array<{ id: string; name: string; createdAt: string }>
  accounts: AccountSummary[]
  buyers: LogicalBuyer[]
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
