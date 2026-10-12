import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { GoogleWorkspaceService } from '@/gen/neobox/v1/google_workspace_pb'
import { makeClient } from './transport'

export type {
  WorkspaceTenant,
  WorkspaceUser,
} from '@/gen/neobox/v1/google_workspace_pb'
const client = makeClient(GoogleWorkspaceService)
const key = (connectionId: string) =>
  ['google-workspace', connectionId] as const

export function useWorkspaceTenants(connectionId: string) {
  return useQuery({
    queryKey: [...key(connectionId), 'tenants'],
    queryFn: async () =>
      (await client.listWorkspaceTenants({ connectionId })).tenants,
    retry: false,
  })
}
export function useWorkspaceTenantChanges(connectionId: string) {
  const qc = useQueryClient()
  const onSettled = () => qc.invalidateQueries({ queryKey: key(connectionId) })
  return {
    create: useMutation({
      mutationFn: (v: {
        name: string
        customerId: string
        delegatedAdminEmail: string
      }) => client.createWorkspaceTenant({ connectionId, ...v }),
      onSettled,
    }),
    update: useMutation({
      mutationFn: (v: {
        tenantId: string
        name: string
        delegatedAdminEmail: string
      }) => client.updateWorkspaceTenant({ connectionId, ...v }),
      onSettled,
    }),
    test: useMutation({
      mutationFn: (tenantId: string) =>
        client.testWorkspaceTenant({ connectionId, tenantId }),
      onSettled,
    }),
    remove: useMutation({
      mutationFn: (tenantId: string) =>
        client.deleteWorkspaceTenant({ connectionId, tenantId }),
      onSettled: async (_d, error, tenantId) => {
        await onSettled()
        if (!error)
          qc.removeQueries({ queryKey: [...key(connectionId), tenantId] })
      },
    }),
  }
}
export function useWorkspaceUsers(
  connectionId: string,
  tenantId: string,
  query: string,
  pageToken: string
) {
  return useQuery({
    queryKey: [...key(connectionId), tenantId, 'users', query, pageToken],
    queryFn: () =>
      client.listWorkspaceUsers({
        connectionId,
        tenantId,
        query,
        pageToken,
        pageSize: 50,
      }),
    retry: false,
  })
}
export function useWorkspaceUser(
  connectionId: string,
  tenantId: string,
  userId: string
) {
  return useQuery({
    queryKey: [...key(connectionId), tenantId, 'user', userId],
    queryFn: async () =>
      (await client.getWorkspaceUser({ connectionId, tenantId, userId })).user,
    retry: false,
  })
}
export function useWorkspaceUserChanges(
  connectionId: string,
  tenantId: string
) {
  const qc = useQueryClient()
  const onSettled = () =>
    qc.invalidateQueries({ queryKey: [...key(connectionId), tenantId] })
  const base = { connectionId, tenantId }
  type Profile = { givenName: string; familyName: string; orgUnitPath: string }
  return {
    create: useMutation({
      mutationFn: (v: {
        primaryEmail: string
        profile: Profile
        password: string
        changePasswordAtNextLogin: boolean
      }) => client.createWorkspaceUser({ ...base, ...v }),
      onSettled,
      gcTime: 0,
    }),
    update: useMutation({
      mutationFn: (v: { userId: string; profile: Profile }) =>
        client.updateWorkspaceUser({ ...base, ...v }),
      onSettled,
    }),
    password: useMutation({
      mutationFn: (v: {
        userId: string
        password: string
        changePasswordAtNextLogin: boolean
      }) => client.resetWorkspaceUserPassword({ ...base, ...v }),
      onSettled,
      gcTime: 0,
    }),
    suspend: useMutation({
      mutationFn: (v: { userId: string; suspended: boolean }) =>
        client.setWorkspaceUserSuspended({ ...base, ...v }),
      onSettled,
    }),
  }
}
