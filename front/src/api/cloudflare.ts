import {
  keepPreviousData,
  useIsFetching,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import {
  CloudflareRecordAccess,
  CloudflareService,
  type CloudflareChangeResult,
  type CloudflareDNSRecordInputSchema,
  type CloudflarePagesEnvironment,
  type CloudflareResourceKind,
  type CloudflareZone,
} from '@/gen/neobox/v1/cloudflare_pb'
import type { MessageInitShape } from '@bufbuild/protobuf'
import { Code, ConnectError, makeClient } from './transport'

export {
  CloudflareAddressKind,
  CloudflareRecordAccess,
  CloudflareRecordAction,
  CloudflareOperationStatus,
  type CloudflareAddress,
  type CloudflareAddressIssue,
  type CloudflareDNSOperation,
  type CloudflareDNSRecord,
  type CloudflarePageInfo,
  type CloudflarePagesProject,
  type CloudflareWorker,
  type CloudflareZone,
  CloudflareTunnelRecordStatus,
  CloudflareTunnelStatus,
  type CloudflareReadIssue,
  type CloudflareTunnel,
  type CloudflareTunnelConnector,
  type CloudflareTunnelHostname,
  type CloudflareTunnelRecordResult,
  type GetCloudflareTunnelResponse,
  CloudflarePagesEnvironment,
  CloudflarePagesEnvVarType,
  CloudflareResourceKind,
  type CloudflareChangeResult,
  type CloudflarePagesBuildConfig,
  type CloudflarePagesDeployment,
  type CloudflarePagesDomain,
  type CloudflarePagesEnvVar,
  type CloudflareResourceOperation,
  type CloudflareWorkerDeployment,
  type CloudflareWorkerDomain,
  type CloudflareWorkerRoute,
  type CloudflareWorkerSecret,
  type CloudflareWorkerVersion,
  type GetCloudflarePagesProjectResponse,
  type GetCloudflareWorkerResponse,
} from '@/gen/neobox/v1/cloudflare_pb'

/** A DNS record as sent on create and update. */
export type CloudflareDNSRecordInit = MessageInitShape<
  typeof CloudflareDNSRecordInputSchema
>

const client = makeClient(CloudflareService)

/** A search and page of a list. */
export type ListQuery = { query: string; page: number; pageSize: number }

const keys = {
  all: (connectionId: string) => ['cloudflare', connectionId] as const,
  zones: (connectionId: string, q: ListQuery) =>
    ['cloudflare', connectionId, 'zones', q] as const,
  zone: (connectionId: string, zoneId: string) =>
    ['cloudflare', connectionId, 'zone', zoneId] as const,
  records: (
    connectionId: string,
    zoneId: string,
    q?: ListQuery & { type: string }
  ) =>
    q
      ? (['cloudflare', connectionId, 'records', zoneId, q] as const)
      : (['cloudflare', connectionId, 'records', zoneId] as const),
  operations: (connectionId: string, zoneId: string, page?: number) =>
    page === undefined
      ? (['cloudflare', connectionId, 'operations', zoneId] as const)
      : (['cloudflare', connectionId, 'operations', zoneId, page] as const),
  workers: (connectionId: string, q: ListQuery) =>
    ['cloudflare', connectionId, 'workers', q] as const,
  pages: (connectionId: string, q: ListQuery) =>
    ['cloudflare', connectionId, 'pages', q] as const,
  tunnels: (connectionId: string, q?: ListQuery) =>
    q
      ? (['cloudflare', connectionId, 'tunnels', q] as const)
      : (['cloudflare', connectionId, 'tunnels'] as const),
  tunnel: (connectionId: string, tunnelId: string) =>
    ['cloudflare', connectionId, 'tunnel', tunnelId] as const,
  worker: (connectionId: string, name: string) =>
    ['cloudflare', connectionId, 'worker', name] as const,
  project: (connectionId: string, name: string) =>
    ['cloudflare', connectionId, 'project', name] as const,
  deployments: (
    connectionId: string,
    name: string,
    env?: CloudflarePagesEnvironment,
    page?: number
  ) =>
    env === undefined
      ? (['cloudflare', connectionId, 'deployments', name] as const)
      : ([
          'cloudflare',
          connectionId,
          'deployments',
          name,
          env,
          page ?? 1,
        ] as const),
  resourceOps: (
    connectionId: string,
    kind?: CloudflareResourceKind,
    resource?: string,
    page?: number
  ) =>
    kind === undefined
      ? (['cloudflare', connectionId, 'resource-ops'] as const)
      : ([
          'cloudflare',
          connectionId,
          'resource-ops',
          kind,
          resource ?? '',
          page ?? 1,
        ] as const),
}

// Everything is read live from Cloudflare when a page opens; there is no
// background sync. Lists keep the previous page on screen while the next
// one loads, and are refreshed by hand.

export function useCloudflareZones(connectionId: string, q: ListQuery) {
  return useQuery({
    queryKey: keys.zones(connectionId, q),
    queryFn: async () =>
      await client.listCloudflareZones({ connectionId, ...q }),
    placeholderData: keepPreviousData,
    retry: false,
  })
}

export function useCloudflareZone(connectionId: string, zoneId: string) {
  return useQuery({
    queryKey: keys.zone(connectionId, zoneId),
    queryFn: async () =>
      (await client.getCloudflareZone({ connectionId, zoneId })).zone,
    retry: false,
  })
}

export function useCloudflareDNSRecords(
  connectionId: string,
  zoneId: string,
  q: ListQuery & { type: string }
) {
  return useQuery({
    queryKey: keys.records(connectionId, zoneId, q),
    queryFn: async () =>
      await client.listCloudflareDNSRecords({ connectionId, zoneId, ...q }),
    placeholderData: keepPreviousData,
    retry: false,
  })
}

export function useCloudflareDNSOperations(
  connectionId: string,
  zoneId: string,
  page: number,
  enabled = true
) {
  return useQuery({
    queryKey: keys.operations(connectionId, zoneId, page),
    queryFn: async () =>
      await client.listCloudflareDNSOperations({
        connectionId,
        zoneId,
        page,
        pageSize: 20,
      }),
    placeholderData: keepPreviousData,
    enabled,
  })
}

export function useCloudflareWorkers(connectionId: string, q: ListQuery) {
  return useQuery({
    queryKey: keys.workers(connectionId, q),
    queryFn: async () =>
      await client.listCloudflareWorkers({ connectionId, ...q }),
    placeholderData: keepPreviousData,
    retry: false,
  })
}

export function useCloudflarePagesProjects(connectionId: string, q: ListQuery) {
  return useQuery({
    queryKey: keys.pages(connectionId, q),
    queryFn: async () =>
      await client.listCloudflarePagesProjects({ connectionId, ...q }),
    placeholderData: keepPreviousData,
    retry: false,
  })
}

export function useCloudflareTunnels(connectionId: string, q: ListQuery) {
  return useQuery({
    queryKey: keys.tunnels(connectionId, q),
    queryFn: async () =>
      await client.listCloudflareTunnels({ connectionId, ...q }),
    placeholderData: keepPreviousData,
    retry: false,
  })
}

export function useCloudflareTunnel(connectionId: string, tunnelId: string) {
  return useQuery({
    queryKey: keys.tunnel(connectionId, tunnelId),
    queryFn: async () =>
      await client.getCloudflareTunnel({ connectionId, tunnelId }),
    retry: false,
  })
}

/**
 * Reads a Tunnel's token on demand. It is a credential, so it is neither
 * cached nor kept: the caller holds it only while it is shown.
 */
export function useCloudflareTunnelToken(connectionId: string) {
  return useMutation({
    mutationFn: async (tunnelId: string) =>
      (await client.getCloudflareTunnelToken({ connectionId, tunnelId })).token,
    gcTime: 0,
  })
}

/** Create, delete and clean up Tunnels; the lists are read again after. */
export function useCloudflareTunnelChanges(connectionId: string) {
  const qc = useQueryClient()
  const onSettled = () =>
    qc.invalidateQueries({ queryKey: keys.tunnels(connectionId) })
  return {
    create: useMutation({
      mutationFn: async (name: string) =>
        (await client.createCloudflareTunnel({ connectionId, name })).tunnel,
      onSettled,
    }),
    remove: useMutation({
      mutationFn: async (v: {
        tunnelId: string
        cleanupConnections: boolean
      }) => await client.deleteCloudflareTunnel({ connectionId, ...v }),
      onSettled: async () => {
        await onSettled()
        qc.removeQueries({ queryKey: ['cloudflare', connectionId, 'tunnel'] })
      },
    }),
    cleanup: useMutation({
      mutationFn: async (tunnelId: string) =>
        await client.cleanupCloudflareTunnelConnections({
          connectionId,
          tunnelId,
        }),
      onSettled: async (_d, _e, tunnelId) => {
        await Promise.all([
          onSettled(),
          qc.invalidateQueries({
            queryKey: keys.tunnel(connectionId, tunnelId),
          }),
        ])
      },
    }),
  }
}

/**
 * A Tunnel's public hostnames. A change may have been made even when it
 * fails with an unconfirmed result, so the Tunnel is read again either way.
 */
export function useCloudflareTunnelHostnames(
  connectionId: string,
  tunnelId: string
) {
  const qc = useQueryClient()
  const onSettled = () =>
    qc.invalidateQueries({ queryKey: keys.tunnel(connectionId, tunnelId) })
  const base = { connectionId, tunnelId }
  return {
    add: useMutation({
      mutationFn: async (v: {
        hostname: string
        path: string
        service: string
        createDns: boolean
      }) => await client.addCloudflareTunnelHostname({ ...base, ...v }),
      onSettled,
    }),
    update: useMutation({
      mutationFn: async (v: {
        hostname: string
        currentPath: string
        path: string
        service: string
      }) => await client.updateCloudflareTunnelHostname({ ...base, ...v }),
      onSettled,
    }),
    remove: useMutation({
      mutationFn: async (v: {
        hostname: string
        path: string
        deleteDns: boolean
      }) => await client.deleteCloudflareTunnelHostname({ ...base, ...v }),
      onSettled,
    }),
  }
}

export function useCloudflareWorker(connectionId: string, name: string) {
  return useQuery({
    queryKey: keys.worker(connectionId, name),
    queryFn: async () =>
      await client.getCloudflareWorker({ connectionId, name }),
    retry: false,
  })
}

export function useCloudflarePagesProject(connectionId: string, name: string) {
  return useQuery({
    queryKey: keys.project(connectionId, name),
    queryFn: async () =>
      await client.getCloudflarePagesProject({ connectionId, name }),
    retry: false,
  })
}

export function useCloudflarePagesDeployments(
  connectionId: string,
  name: string,
  environment: CloudflarePagesEnvironment,
  page: number
) {
  return useQuery({
    queryKey: keys.deployments(connectionId, name, environment, page),
    queryFn: async () =>
      await client.listCloudflarePagesDeployments({
        connectionId,
        name,
        environment,
        page,
        pageSize: 20,
      }),
    placeholderData: keepPreviousData,
    retry: false,
  })
}

export function useCloudflareResourceOperations(
  connectionId: string,
  kind: CloudflareResourceKind,
  resource: string,
  page: number
) {
  return useQuery({
    queryKey: keys.resourceOps(connectionId, kind, resource, page),
    queryFn: async () =>
      await client.listCloudflareResourceOperations({
        connectionId,
        kind,
        resource,
        page,
        pageSize: 20,
      }),
    placeholderData: keepPreviousData,
  })
}

/**
 * Builds the mutations of one resource's changes. Whatever the outcome (a
 * failed or unconfirmed change may still have been made and logged), what
 * the resource shows and its log are read again. Callers show the error
 * where the change was made, so the app-wide toast stays quiet.
 */
function useResourceChanges(
  connectionId: string,
  invalidate: readonly (readonly unknown[])[]
) {
  const qc = useQueryClient()
  const onSettled = () =>
    Promise.all([
      ...invalidate.map((queryKey) => qc.invalidateQueries({ queryKey })),
      qc.invalidateQueries({ queryKey: keys.resourceOps(connectionId) }),
      qc.invalidateQueries({
        queryKey: ['cloudflare', connectionId, 'workers'],
      }),
      qc.invalidateQueries({ queryKey: ['cloudflare', connectionId, 'pages'] }),
    ])
  return function change<V, R extends { result?: CloudflareChangeResult }>(
    fn: (v: V) => Promise<R>
  ) {
    return {
      mutationFn: fn,
      onSettled,
    }
  }
}

/** Every change to a Worker. */
export function useCloudflareWorkerChanges(connectionId: string, name: string) {
  const change = useResourceChanges(connectionId, [
    keys.worker(connectionId, name),
  ])
  const base = { connectionId, name }
  return {
    setWorkersDev: useMutation(
      change((enabled: boolean) =>
        client.setCloudflareWorkerWorkersDev({ ...base, enabled })
      )
    ),
    attachDomain: useMutation(
      change((hostname: string) =>
        client.attachCloudflareWorkerDomain({ ...base, hostname })
      )
    ),
    detachDomain: useMutation(
      change((hostname: string) =>
        client.detachCloudflareWorkerDomain({ ...base, hostname })
      )
    ),
    addRoute: useMutation(
      change((pattern: string) =>
        client.addCloudflareWorkerRoute({ ...base, pattern })
      )
    ),
    deleteRoute: useMutation(
      change((v: { zoneId: string; routeId: string }) =>
        client.deleteCloudflareWorkerRoute({ ...base, ...v })
      )
    ),
    addCron: useMutation(
      change((cron: string) =>
        client.addCloudflareWorkerCron({ ...base, cron })
      )
    ),
    deleteCron: useMutation(
      change((cron: string) =>
        client.deleteCloudflareWorkerCron({ ...base, cron })
      )
    ),
    putSecret: useMutation(
      change((v: { secretName: string; value: string }) =>
        client.putCloudflareWorkerSecret({ ...base, ...v })
      )
    ),
    deleteSecret: useMutation(
      change((secretName: string) =>
        client.deleteCloudflareWorkerSecret({ ...base, secretName })
      )
    ),
    rollback: useMutation(
      change((versionId: string) =>
        client.rollbackCloudflareWorker({ ...base, versionId })
      )
    ),
    remove: useMutation(
      change(() => client.deleteCloudflareWorker({ ...base }))
    ),
  }
}

/** Creates a Pages project; the Pages list is read again after. */
export function useCloudflarePagesProjectCreate(connectionId: string) {
  const change = useResourceChanges(connectionId, [])
  return useMutation(
    change((v: { name: string; productionBranch: string }) =>
      client.createCloudflarePagesProject({ connectionId, ...v })
    )
  )
}

/** Every change to a Pages project. */
export function useCloudflarePagesChanges(connectionId: string, name: string) {
  const change = useResourceChanges(connectionId, [
    keys.project(connectionId, name),
    keys.deployments(connectionId, name),
  ])
  const base = { connectionId, name }
  return {
    update: useMutation(
      change(
        (v: {
          productionBranch?: string
          buildCommand?: string
          destinationDir?: string
          rootDir?: string
        }) => client.updateCloudflarePagesProject({ ...base, ...v })
      )
    ),
    remove: useMutation(
      change(() => client.deleteCloudflarePagesProject({ ...base }))
    ),
    retry: useMutation(
      change((deploymentId: string) =>
        client.retryCloudflarePagesDeployment({ ...base, deploymentId })
      )
    ),
    rollback: useMutation(
      change((deploymentId: string) =>
        client.rollbackCloudflarePagesDeployment({ ...base, deploymentId })
      )
    ),
    deleteDeployment: useMutation(
      change((deploymentId: string) =>
        client.deleteCloudflarePagesDeployment({ ...base, deploymentId })
      )
    ),
    addDomain: useMutation(
      change((v: { hostname: string; createDns: boolean }) =>
        client.addCloudflarePagesDomain({ ...base, ...v })
      )
    ),
    deleteDomain: useMutation(
      change((hostname: string) =>
        client.deleteCloudflarePagesDomain({ ...base, hostname })
      )
    ),
    setEnvVar: useMutation(
      change(
        (v: {
          environment: CloudflarePagesEnvironment
          varName: string
          value: string
          secret: boolean
        }) => client.setCloudflarePagesEnvVar({ ...base, ...v })
      )
    ),
    deleteEnvVar: useMutation(
      change(
        (v: { environment: CloudflarePagesEnvironment; varName: string }) =>
          client.deleteCloudflarePagesEnvVar({ ...base, ...v })
      )
    ),
  }
}

/**
 * Whether Cloudflare refused a change for lack of permission: proof the
 * token may not change the Zone's records.
 */
export function isPermissionRefusal(err: unknown) {
  return err instanceof ConnectError && err.code === Code.PermissionDenied
}

/**
 * The DNS changes. Whatever the outcome (a failed or unconfirmed change may
 * still have been logged), the Zone's records and operation log are read
 * again.
 */
export function useCloudflareDNSChanges(connectionId: string, zoneId: string) {
  const qc = useQueryClient()
  const onSettled = () =>
    Promise.all([
      qc.invalidateQueries({ queryKey: keys.records(connectionId, zoneId) }),
      qc.invalidateQueries({
        queryKey: keys.operations(connectionId, zoneId),
      }),
    ])
  const base = { connectionId, zoneId }
  // Callers show the error where the change was made (the form, the delete
  // dialog), so the app-wide error toast stays quiet. A refusal for lack of
  // permission makes the Zone read-only here until it is read again.
  const onError = (err: unknown) => {
    if (!isPermissionRefusal(err)) return
    qc.setQueryData<CloudflareZone>(
      keys.zone(connectionId, zoneId),
      (z) => z && { ...z, dnsAccess: CloudflareRecordAccess.READ }
    )
  }
  return {
    create: useMutation({
      mutationFn: async (record: CloudflareDNSRecordInit) =>
        await client.createCloudflareDNSRecord({ ...base, record }),
      onError,
      onSettled,
    }),
    update: useMutation({
      mutationFn: async (v: {
        recordId: string
        record: CloudflareDNSRecordInit
      }) => await client.updateCloudflareDNSRecord({ ...base, ...v }),
      onError,
      onSettled,
    }),
    setProxied: useMutation({
      mutationFn: async (v: { recordId: string; proxied: boolean }) =>
        await client.setCloudflareDNSRecordProxied({ ...base, ...v }),
      onError,
      onSettled,
    }),
    remove: useMutation({
      mutationFn: async (recordId: string) =>
        await client.deleteCloudflareDNSRecord({ ...base, recordId }),
      onError,
      onSettled,
    }),
  }
}

/** Re-reads everything shown for the connection. */
export function useRefreshCloudflare(connectionId: string) {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: keys.all(connectionId) })
}

/** How many of the connection's reads are in flight. */
export function useCloudflareFetching(connectionId: string) {
  return useIsFetching({ queryKey: keys.all(connectionId) })
}
