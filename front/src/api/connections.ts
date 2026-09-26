import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ConnectionService,
  Provider,
  type CreateConnectionRequestSchema,
} from '@/gen/neobox/v1/connection_pb'
import type { MessageInitShape } from '@bufbuild/protobuf'
import { makeClient } from './transport'

export {
  ConnectionStatus,
  Provider,
  type Connection,
} from '@/gen/neobox/v1/connection_pb'

/** A provider's settings as sent on create and update. */
export type ConnectionSettings = MessageInitShape<
  typeof CreateConnectionRequestSchema
>['settings']

const client = makeClient(ConnectionService)

const keys = {
  all: ['connections'] as const,
  list: (provider: Provider) => ['connections', 'list', provider] as const,
  one: (id: string) => ['connections', 'one', id] as const,
}

export function useConnections(provider: Provider = Provider.UNSPECIFIED) {
  return useQuery({
    queryKey: keys.list(provider),
    queryFn: async () =>
      (await client.listConnections({ provider })).connections,
  })
}

export function useConnection(id: string) {
  return useQuery({
    queryKey: keys.one(id),
    queryFn: async () => (await client.getConnection({ id })).connection,
  })
}

export function useCreateConnection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { name: string; settings: ConnectionSettings }) =>
      (await client.createConnection(input)).connection,
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.all }),
  })
}

export function useUpdateConnection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      id: string
      name: string
      settings: ConnectionSettings
    }) => (await client.updateConnection(input)).connection,
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.all }),
  })
}

export function useDeleteConnection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      await client.deleteConnection({ id })
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.all }),
  })
}

/** Re-checks the stored settings; the result lands in the connection's status. */
export function useTestConnection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) =>
      (await client.testConnection({ id })).connection,
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.all }),
  })
}
