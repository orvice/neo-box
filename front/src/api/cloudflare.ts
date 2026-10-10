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
  type CloudflareDNSRecordInputSchema,
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
