import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  NocoDBService,
  SnapshotStatus,
  type Snapshot,
} from '@/gen/neobox/v1/nocodb_pb'
import { TOKEN_KEY } from '@/lib/constants'
import { BASE_URL, makeClient } from './transport'

export {
  SnapshotStatus,
  SnapshotTrigger,
  type BackupPolicy,
  type NocoDBBase,
  type Snapshot,
  type SnapshotTable,
} from '@/gen/neobox/v1/nocodb_pb'

const client = makeClient(NocoDBService)

const keys = {
  bases: (connectionId: string) => ['nocodb', 'bases', connectionId] as const,
  snapshots: (connectionId?: string, baseId?: string) =>
    ['nocodb', 'snapshots', connectionId ?? '', baseId ?? ''] as const,
  snapshot: (id: string) => ['nocodb', 'snapshot', id] as const,
  records: (snapshotId: string, tableId: string, page: number, size: number) =>
    ['nocodb', 'records', snapshotId, tableId, page, size] as const,
}

export function isSnapshotActive(s: Snapshot | undefined) {
  return (
    s?.status === SnapshotStatus.PENDING || s?.status === SnapshotStatus.RUNNING
  )
}

// --- bases & policies ---

export function useNocoDBBases(connectionId: string, enabled = true) {
  return useQuery({
    queryKey: keys.bases(connectionId),
    queryFn: async () => (await client.listNocoDBBases({ connectionId })).bases,
    enabled,
    // Keep latest-snapshot status fresh while a run is in progress.
    refetchInterval: (query) =>
      query.state.data?.some((b) => isSnapshotActive(b.latestSnapshot))
        ? 3000
        : false,
  })
}

export function useUpsertBackupPolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      connectionId: string
      baseId: string
      enabled: boolean
      cron: string
      retention: number
    }) => (await client.upsertBackupPolicy(input)).policy,
    onSuccess: (_data, input) =>
      qc.invalidateQueries({ queryKey: keys.bases(input.connectionId) }),
  })
}

// --- snapshots ---

export function useCreateSnapshot() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { connectionId: string; baseId: string }) =>
      (await client.createSnapshot(input)).snapshot,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['nocodb'] }),
  })
}

export function useSnapshots(connectionId?: string, baseId?: string) {
  return useQuery({
    queryKey: keys.snapshots(connectionId, baseId),
    queryFn: async () =>
      (await client.listSnapshots({ connectionId, baseId, limit: 100 }))
        .snapshots,
    refetchInterval: (query) =>
      query.state.data?.some(isSnapshotActive) ? 3000 : false,
  })
}

export function useSnapshot(id: string) {
  return useQuery({
    queryKey: keys.snapshot(id),
    queryFn: async () => (await client.getSnapshot({ id })).snapshot,
    refetchInterval: (query) =>
      isSnapshotActive(query.state.data) ? 2000 : false,
  })
}

export function useDeleteSnapshot() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      await client.deleteSnapshot({ id })
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['nocodb'] }),
  })
}

export function useSnapshotRecords(
  snapshotId: string,
  tableId: string | undefined,
  page: number,
  pageSize: number
) {
  return useQuery({
    queryKey: keys.records(snapshotId, tableId ?? '', page, pageSize),
    queryFn: async () =>
      await client.listSnapshotRecords({
        snapshotId,
        tableId: tableId ?? '',
        page,
        pageSize,
      }),
    enabled: !!tableId,
    placeholderData: (prev) => prev,
  })
}

/** Downloads the snapshot's gzip JSON through an authenticated fetch. */
export async function downloadSnapshot(id: string) {
  const token = localStorage.getItem(TOKEN_KEY)
  const res = await fetch(`${BASE_URL}/api/nocodb/snapshots/${id}/download`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new Error(body?.error || `Download failed (${res.status})`)
  }
  const disposition = res.headers.get('Content-Disposition') ?? ''
  const name =
    /filename="([^"]+)"/.exec(disposition)?.[1] ?? `snapshot-${id}.json.gz`
  const url = URL.createObjectURL(await res.blob())
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}
