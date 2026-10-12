import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { GoogleWorkspaceService } from '@/gen/neobox/v1/google_workspace_pb'
import { makeClient } from './transport'

export type {
  WorkspaceTenant,
  WorkspaceUser,
  WorkspaceAlias,
  WorkspaceGroup,
  WorkspaceGroupMember,
  WorkspaceOrgUnit,
  WorkspaceUserToken,
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

export function useWorkspaceAliases(
  connectionId: string,
  tenantId: string,
  userId: string
) {
  return useQuery({
    queryKey: [...key(connectionId), tenantId, 'aliases', userId],
    queryFn: async () =>
      (
        await client.listWorkspaceUserAliases({
          connectionId,
          tenantId,
          userId,
        })
      ).aliases,
    retry: false,
  })
}
export function useWorkspaceGroups(
  connectionId: string,
  tenantId: string,
  query = '',
  pageToken = ''
) {
  return useQuery({
    queryKey: [...key(connectionId), tenantId, 'groups', query, pageToken],
    queryFn: () =>
      client.listWorkspaceGroups({
        connectionId,
        tenantId,
        query,
        pageToken,
        pageSize: 50,
      }),
    retry: false,
  })
}
export function useWorkspaceUserGroups(
  connectionId: string,
  tenantId: string,
  userId: string
) {
  return useQuery({
    queryKey: [...key(connectionId), tenantId, 'user-groups', userId],
    queryFn: async () =>
      (await client.listWorkspaceUserGroups({ connectionId, tenantId, userId }))
        .groups,
    retry: false,
  })
}
export function useWorkspaceMembers(
  connectionId: string,
  tenantId: string,
  groupId: string,
  pageToken: string
) {
  return useQuery({
    queryKey: [...key(connectionId), tenantId, 'members', groupId, pageToken],
    queryFn: () =>
      client.listWorkspaceGroupMembers({
        connectionId,
        tenantId,
        groupId,
        pageToken,
        pageSize: 50,
      }),
    retry: false,
  })
}
export function useWorkspaceOrgUnits(
  connectionId: string,
  tenantId: string,
  enabled = true
) {
  return useQuery({
    queryKey: [...key(connectionId), tenantId, 'org-units'],
    queryFn: async () =>
      (await client.listWorkspaceOrgUnits({ connectionId, tenantId })).orgUnits,
    retry: false,
    enabled,
  })
}
export function useWorkspaceTokens(
  connectionId: string,
  tenantId: string,
  userId: string
) {
  return useQuery({
    queryKey: [...key(connectionId), tenantId, 'tokens', userId],
    queryFn: async () =>
      (await client.listWorkspaceUserTokens({ connectionId, tenantId, userId }))
        .tokens,
    retry: false,
  })
}
export function useWorkspaceDirectoryChanges(
  connectionId: string,
  tenantId: string
) {
  const qc = useQueryClient()
  const base = { connectionId, tenantId }
  // A server error may be an unconfirmed write; re-read every affected list.
  const onSettled = () =>
    qc.invalidateQueries({ queryKey: [...key(connectionId), tenantId] })
  type GroupInput = { email: string; name: string; description: string }
  type OrgInput = { name: string; description: string; parentPath: string }
  return {
    addAlias: useMutation({
      mutationFn: (v: { userId: string; alias: string }) =>
        client.addWorkspaceUserAlias({ ...base, ...v }),
      onSettled,
    }),
    deleteAlias: useMutation({
      mutationFn: (v: { userId: string; alias: string }) =>
        client.deleteWorkspaceUserAlias({ ...base, ...v }),
      onSettled,
    }),
    createGroup: useMutation({
      mutationFn: (group: GroupInput) =>
        client.createWorkspaceGroup({ ...base, group }),
      onSettled,
    }),
    updateGroup: useMutation({
      mutationFn: (v: { groupId: string; group: GroupInput }) =>
        client.updateWorkspaceGroup({ ...base, ...v }),
      onSettled,
    }),
    deleteGroup: useMutation({
      mutationFn: (groupId: string) =>
        client.deleteWorkspaceGroup({ ...base, groupId }),
      onSettled,
    }),
    addMember: useMutation({
      mutationFn: (v: { groupId: string; email: string; role: string }) =>
        client.addWorkspaceGroupMember({ ...base, ...v }),
      onSettled,
    }),
    updateMember: useMutation({
      mutationFn: (v: { groupId: string; memberId: string; role: string }) =>
        client.updateWorkspaceGroupMember({ ...base, ...v }),
      onSettled,
    }),
    deleteMember: useMutation({
      mutationFn: (v: { groupId: string; memberId: string }) =>
        client.deleteWorkspaceGroupMember({ ...base, ...v }),
      onSettled,
    }),
    createOrgUnit: useMutation({
      mutationFn: (orgUnit: OrgInput) =>
        client.createWorkspaceOrgUnit({ ...base, orgUnit }),
      onSettled,
    }),
    updateOrgUnit: useMutation({
      mutationFn: (v: { orgUnitId: string; orgUnit: OrgInput }) =>
        client.updateWorkspaceOrgUnit({ ...base, ...v }),
      onSettled,
    }),
    deleteOrgUnit: useMutation({
      mutationFn: (orgUnitId: string) =>
        client.deleteWorkspaceOrgUnit({ ...base, orgUnitId }),
      onSettled,
    }),
    signOut: useMutation({
      mutationFn: (userId: string) =>
        client.signOutWorkspaceUser({ ...base, userId }),
      onSettled,
    }),
    revokeToken: useMutation({
      mutationFn: (v: { userId: string; clientId: string }) =>
        client.revokeWorkspaceUserToken({ ...base, ...v }),
      onSettled,
    }),
  }
}
