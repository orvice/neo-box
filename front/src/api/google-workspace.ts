import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { GoogleWorkspaceService } from '@/gen/neobox/v1/google_workspace_pb'
import { timestampFromDate } from '@bufbuild/protobuf/wkt'
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
  const onSettled = () =>
    Promise.all([
      qc.invalidateQueries({ queryKey: key(connectionId) }),
      qc.invalidateQueries({ queryKey: ['google-workspace', 'overview'] }),
    ])
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
    Promise.all([
      qc.invalidateQueries({ queryKey: key(connectionId) }),
      qc.invalidateQueries({ queryKey: ['google-workspace', 'overview'] }),
    ])
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
    Promise.all([
      qc.invalidateQueries({ queryKey: key(connectionId) }),
      qc.invalidateQueries({ queryKey: ['google-workspace', 'overview'] }),
    ])
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

export {
  WorkspaceOperationStatus,
  type WorkspaceOperation,
  type WorkspaceTenantSummary,
  type WorkspaceOverviewUser,
  type WorkspaceAuditActivity,
} from '@/gen/neobox/v1/google_workspace_pb'
export type WorkspaceOverviewQuery = {
  connectionId: string
  tenantId: string
  query: string
  securityFilter: string
  inactivityDays: number
  includeGroups: boolean
  page: number
  pageSize: number
}
export function useWorkspaceOverview(q: WorkspaceOverviewQuery) {
  return useQuery({
    queryKey: ['google-workspace', 'overview', q],
    queryFn: () => client.getWorkspaceOverview(q),
    retry: false,
    staleTime: 60000,
    refetchOnWindowFocus: false,
  })
}
export function useWorkspaceOperations(
  connectionId: string,
  tenantId: string,
  query: string,
  status: import('@/gen/neobox/v1/google_workspace_pb').WorkspaceOperationStatus,
  page: number
) {
  return useQuery({
    queryKey: [
      ...key(connectionId),
      'operations',
      tenantId,
      query,
      status,
      page,
    ],
    queryFn: () =>
      client.listWorkspaceOperations({
        connectionId,
        tenantId,
        query,
        status,
        page,
        pageSize: 20,
      }),
    retry: false,
  })
}
export type WorkspaceAuditQuery = {
  connectionId: string
  tenantId: string
  application: string
  userKey: string
  startTime: string
  endTime: string
  eventName: string
  pageToken: string
  pageSize: number
}
export function useWorkspaceAudit(q: WorkspaceAuditQuery) {
  return useQuery({
    queryKey: [...key(q.connectionId), q.tenantId, 'google-audit', q],
    queryFn: () => client.listWorkspaceAuditActivities(q),
    retry: false,
    refetchOnWindowFocus: false,
  })
}

export type WorkspaceDriveActivityQuery = {
  connectionId: string
  tenantId: string
  preset: string
  userKey: string
  startTime: string
  endTime: string
  includeSideEffects: boolean
  pageToken: string
  pageSize: number
}
export function useWorkspaceDriveActivities(q: WorkspaceDriveActivityQuery) {
  return useQuery({
    queryKey: [...key(q.connectionId), q.tenantId, 'drive-activities', q],
    queryFn: () => client.listWorkspaceDriveActivities(q),
    retry: false,
    refetchOnWindowFocus: false,
  })
}
export type WorkspaceDriveUsageQuery = {
  connectionId: string
  tenantId: string
  query: string
  sort: string
  page: number
  pageSize: number
}
// Usage is a daily report that lags; refetching on focus would only repeat it.
export function useWorkspaceDriveUsage(q: WorkspaceDriveUsageQuery) {
  return useQuery({
    queryKey: [...key(q.connectionId), q.tenantId, 'drive-usage', q],
    queryFn: () => client.listWorkspaceDriveUsage(q),
    retry: false,
    staleTime: 60000,
    refetchOnWindowFocus: false,
  })
}
export type {
  WorkspaceDriveActivity,
  WorkspaceDriveEvent,
  WorkspaceDriveUsageUser,
} from '@/gen/neobox/v1/google_workspace_pb'

// Drive API reads (docs/adr/0009) are recorded by the backend each time they
// are sent, so nothing here refetches on its own. Results stay cached for a few
// minutes so paging back or switching tabs doesn't send (and log) another read;
// "Read again" does.
const driveReadOptions = {
  retry: false,
  staleTime: Infinity,
  gcTime: 5 * 60_000,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
  refetchOnMount: false,
} as const

export type SharedDriveFilters = {
  namePrefix: string
  noOrganizer: boolean
  maxMembers?: number
  orgUnitId: string
  pageToken: string
}

export function useWorkspaceSharedDrives(
  connectionId: string,
  tenantId: string,
  reason: string | null,
  nonce: number,
  f: SharedDriveFilters
) {
  return useQuery({
    queryKey: [
      'google-workspace-drive',
      'shared-drives',
      connectionId,
      tenantId,
      nonce,
      f,
      reason,
    ],
    queryFn: () =>
      client.listWorkspaceSharedDrives({
        connectionId,
        tenantId,
        reason: reason ?? '',
        pageSize: 50,
        ...f,
      }),
    enabled: reason !== null,
    ...driveReadOptions,
  })
}

export type ExposedFilesFilters = {
  exposures: string[]
  pageToken: string
}

export function useWorkspaceExposedFiles(
  connectionId: string,
  tenantId: string,
  userId: string,
  access: GmailAccess | null,
  nonce: number,
  f: ExposedFilesFilters
) {
  return useQuery({
    queryKey: [
      'google-workspace-drive',
      'exposed-files',
      connectionId,
      tenantId,
      userId,
      nonce,
      f,
      access,
    ],
    queryFn: () =>
      client.listWorkspaceUserExposedFiles({
        connectionId,
        tenantId,
        userId,
        confirmation: access ?? undefined,
        pageSize: 50,
        ...f,
      }),
    enabled: access !== null,
    ...driveReadOptions,
  })
}

export type {
  WorkspaceSharedDrive,
  WorkspaceExposedFile,
  WorkspaceFileExposure,
} from '@/gen/neobox/v1/google_workspace_pb'

export type {
  GetWorkspaceUserGmailSettingsResponse as GmailSettings,
  GmailVacation,
  GmailSectionState,
  GmailFilter,
  GmailSendAs,
  GmailDelegate,
  GmailForwardingAddress,
} from '@/gen/neobox/v1/google_workspace_pb'

export type GmailAccess = { reason: string; targetEmail: string }

// Every read of a mailbox's settings is logged by the backend, so nothing here
// refetches on its own: no window-focus, reconnect or invalidation refreshes,
// and results are dropped as soon as nothing shows them. `nonce` ties the
// result to one confirmation.
const gmailKey = (
  connectionId: string,
  tenantId: string,
  userId: string,
  nonce: number
) => ['google-workspace-gmail', connectionId, tenantId, userId, nonce] as const

export function useWorkspaceGmailSettings(
  connectionId: string,
  tenantId: string,
  userId: string,
  access: GmailAccess | null,
  nonce: number
) {
  return useQuery({
    queryKey: [...gmailKey(connectionId, tenantId, userId, nonce), access],
    queryFn: () =>
      client.getWorkspaceUserGmailSettings({
        connectionId,
        tenantId,
        userId,
        confirmation: access ?? undefined,
      }),
    enabled: access !== null,
    retry: false,
    gcTime: 0,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchOnMount: false,
  })
}

export type GmailVacationDraft = {
  enabled: boolean
  subject: string
  bodyPlainText: string
  restrictToContacts: boolean
  restrictToDomain: boolean
  startTime?: Date
  endTime?: Date
}

export function useWorkspaceGmailVacation(
  connectionId: string,
  tenantId: string,
  userId: string,
  access: GmailAccess | null
) {
  return useMutation({
    mutationFn: (v: GmailVacationDraft) =>
      client.updateWorkspaceUserGmailVacation({
        connectionId,
        tenantId,
        userId,
        confirmation: access ?? undefined,
        vacation: {
          enabled: v.enabled,
          subject: v.subject,
          bodyPlainText: v.bodyPlainText,
          restrictToContacts: v.restrictToContacts,
          restrictToDomain: v.restrictToDomain,
          startTime: v.startTime ? timestampFromDate(v.startTime) : undefined,
          endTime: v.endTime ? timestampFromDate(v.endTime) : undefined,
        },
      }),
    gcTime: 0,
  })
}

export type {
  GmailMailLabel,
  GmailMessageSummary,
  GmailMessage,
  GmailAttachment,
  SearchWorkspaceUserGmailMessagesResponse as GmailSearchPage,
} from '@/gen/neobox/v1/google_workspace_pb'

// Gmail mail (docs/adr/0010). Like the settings read, every call here is
// recorded by the backend, so nothing refetches on its own and results are
// dropped as soon as nothing shows them. `nonce` ties a result to one
// confirmation.
const mailReadOptions = {
  retry: false,
  gcTime: 0,
  staleTime: Infinity,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
  refetchOnMount: false,
} as const

// `enabled` lets one component keep the label read alive while the user moves
// between views that share it, without the key changing and the read (which
// is recorded) being sent again.
export function useWorkspaceGmailLabels(
  connectionId: string,
  tenantId: string,
  userId: string,
  access: GmailAccess | null,
  nonce: number,
  enabled = true
) {
  return useQuery({
    queryKey: [
      'google-workspace-gmail-mail',
      'labels',
      connectionId,
      tenantId,
      userId,
      nonce,
      access,
    ],
    queryFn: () =>
      client.listWorkspaceUserGmailLabels({
        connectionId,
        tenantId,
        userId,
        confirmation: access ?? undefined,
      }),
    enabled: access !== null && enabled,
    ...mailReadOptions,
  })
}

export type GmailSearchParams = {
  query: string
  labelIds: string[]
  includeSpamTrash: boolean
  pageToken: string
}

// `params` is null until the user presses Search, so opening the tab alone
// never sends (and records) a search.
export function useWorkspaceGmailSearch(
  connectionId: string,
  tenantId: string,
  userId: string,
  access: GmailAccess | null,
  nonce: number,
  params: GmailSearchParams | null
) {
  return useQuery({
    queryKey: [
      'google-workspace-gmail-mail',
      'search',
      connectionId,
      tenantId,
      userId,
      nonce,
      params,
      access,
    ],
    queryFn: () =>
      client.searchWorkspaceUserGmailMessages({
        connectionId,
        tenantId,
        userId,
        confirmation: access ?? undefined,
        pageSize: 25,
        ...params,
      }),
    enabled: access !== null && params !== null,
    ...mailReadOptions,
  })
}

export function useWorkspaceGmailMessage(
  connectionId: string,
  tenantId: string,
  userId: string,
  access: GmailAccess | null,
  nonce: number,
  messageId: string | null
) {
  return useQuery({
    queryKey: [
      'google-workspace-gmail-mail',
      'message',
      connectionId,
      tenantId,
      userId,
      nonce,
      messageId,
      access,
    ],
    queryFn: () =>
      client.getWorkspaceUserGmailMessage({
        connectionId,
        tenantId,
        userId,
        confirmation: access ?? undefined,
        messageId: messageId ?? '',
      }),
    enabled: access !== null && messageId !== null,
    ...mailReadOptions,
  })
}

export function useWorkspaceGmailAttachment(
  connectionId: string,
  tenantId: string,
  userId: string,
  access: GmailAccess | null,
  messageId: string
) {
  return useMutation({
    mutationFn: (partId: string) =>
      client.downloadWorkspaceUserGmailAttachment({
        connectionId,
        tenantId,
        userId,
        confirmation: access ?? undefined,
        messageId,
        partId,
      }),
    gcTime: 0,
  })
}

export type { WorkspaceUserUsageDay } from '@/gen/neobox/v1/google_workspace_pb'

// A user's storage and Gmail counts per day, from Google's usage reports.
// Aggregates only (no mailbox read, nothing recorded), and the report lags by
// days, so a few minutes of cache is plenty and focus never refetches it.
export function useWorkspaceUserUsage(
  connectionId: string,
  tenantId: string,
  userId: string,
  days = 14
) {
  return useQuery({
    queryKey: [...key(connectionId), tenantId, 'user-usage', userId, days],
    queryFn: () =>
      client.getWorkspaceUserUsage({ connectionId, tenantId, userId, days }),
    retry: false,
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
  })
}
