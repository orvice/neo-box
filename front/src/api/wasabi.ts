import { useEffect, useRef } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { WasabiService } from '@/gen/neobox/v1/wasabi_pb'
import { makeClient } from './transport'

export {
  type WasabiBucket,
  type WasabiCostEstimate,
  type WasabiSyncState,
  type WasabiUsage,
} from '@/gen/neobox/v1/wasabi_pb'

const client = makeClient(WasabiService)

const keys = {
  all: (connectionId: string) => ['wasabi', connectionId] as const,
  overview: (connectionId: string) =>
    ['wasabi', connectionId, 'overview'] as const,
  buckets: (connectionId: string, includeDeleted: boolean) =>
    ['wasabi', connectionId, 'buckets', includeDeleted] as const,
  usage: (connectionId: string, bucket: string, from: string, to: string) =>
    ['wasabi', connectionId, 'usage', bucket, from, to] as const,
}

// While a sync runs, poll so the page fills in as days arrive.
const syncingInterval = 3000

export function useWasabiOverview(connectionId: string, enabled = true) {
  const qc = useQueryClient()
  const query = useQuery({
    queryKey: keys.overview(connectionId),
    queryFn: async () => await client.getWasabiOverview({ connectionId }),
    enabled,
    refetchInterval: (query) =>
      query.state.data?.sync?.syncing ? syncingInterval : false,
  })

  // While a sync lands data, and once more when it ends, refresh the
  // connection's usage and bucket queries too.
  const syncing = !!query.data?.sync?.syncing
  const wasSyncing = useRef(syncing)
  useEffect(() => {
    if (syncing || wasSyncing.current) {
      void qc.invalidateQueries({
        predicate: (q) =>
          q.queryKey[0] === 'wasabi' &&
          q.queryKey[1] === connectionId &&
          q.queryKey[2] !== 'overview',
      })
    }
    wasSyncing.current = syncing
  }, [query.dataUpdatedAt, syncing, connectionId, qc])

  return query
}

export function useWasabiBuckets(
  connectionId: string,
  includeDeleted: boolean
) {
  return useQuery({
    queryKey: keys.buckets(connectionId, includeDeleted),
    queryFn: async () =>
      (await client.listWasabiBuckets({ connectionId, includeDeleted }))
        .buckets,
  })
}

/** Daily usage of the account (bucket '') or one bucket; days are UTC. */
export function useWasabiUsage(
  connectionId: string,
  bucket: string,
  from: string,
  to: string
) {
  return useQuery({
    queryKey: keys.usage(connectionId, bucket, from, to),
    queryFn: async () =>
      (await client.getWasabiUsage({ connectionId, bucket, from, to })).days,
    // Keep the previous range on screen while the next one loads.
    placeholderData: (prev) => prev,
  })
}

export function useSyncWasabi() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (connectionId: string) =>
      (await client.syncWasabiConnection({ connectionId })).sync,
    onSuccess: (_data, connectionId) =>
      qc.invalidateQueries({ queryKey: keys.all(connectionId) }),
  })
}
