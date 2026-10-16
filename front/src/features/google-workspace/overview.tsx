import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ConnectionStatus } from '@/api/connections'
import {
  useWorkspaceOverview,
  type WorkspaceOverviewUser,
  type WorkspaceTenantSummary,
} from '@/api/google-workspace'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { DataTable, type Column } from '@/components/data-table'
import { DirectoryError } from './directory-parts'
import {
  formatWorkspaceTime,
  lastLoginText,
  triState,
} from './observability-format'

const filters = [
  ['all', 'All accounts'],
  ['admins', 'Administrators'],
  ['admins_without_two_step', 'Administrators without 2-step'],
  ['inactive', 'Inactive accounts'],
  ['never_logged_in', 'Never logged in'],
  ['suspended', 'Suspended'],
  ['unknown', 'Unknown security data'],
] as const
export function WorkspaceOverview({
  connectionId = '',
  tenantId = '',
  securityOnly = false,
}: {
  connectionId?: string
  tenantId?: string
  securityOnly?: boolean
}) {
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState('all')
  const [days, setDays] = useState(90)
  const [groups, setGroups] = useState(false)
  const [page, setPage] = useState(1)
  const data = useWorkspaceOverview({
    connectionId,
    tenantId,
    query,
    securityFilter: filter,
    inactivityDays: days,
    includeGroups: groups,
    page,
    pageSize: 50,
  })
  const totals = data.data?.totals
  const cards = [
    ['Users observed', totals?.userCount],
    ['Active', totals?.activeCount],
    ['Administrators', totals?.adminCount],
    ['Admins without 2-step', totals?.adminsWithoutTwoStep],
    ['Inactive', totals?.inactiveCount],
    ['Unknown security data', totals?.securityUnknownCount],
  ] as const
  const tenants: Column<WorkspaceTenantSummary>[] = [
    {
      header: 'Tenant',
      cell: (t) => (
        <div>
          <p className='font-medium'>{t.tenant?.name}</p>
          <p className='text-xs text-muted-foreground'>
            {t.tenant?.customerId} · {t.connectionName}
          </p>
        </div>
      ),
    },
    {
      header: 'Last authorization check',
      cell: (t) =>
        `${t.tenant?.status === ConnectionStatus.OK ? 'OK' : t.tenant?.status === ConnectionStatus.ERROR ? 'Error' : 'Not checked'} · ${formatWorkspaceTime(t.tenant?.statusCheckedAt)}${t.tenant?.statusMessage ? ' · ' + t.tenant.statusMessage : ''}`,
    },
    {
      header: 'Users observed',
      cell: (t) => `${t.userCount}${t.usersComplete ? '' : ' (partial)'}`,
    },
    {
      header: 'Groups observed',
      cell: (t) =>
        t.groupCount === undefined
          ? 'Not loaded'
          : `${t.groupCount}${t.groupsComplete ? '' : ' (partial)'}`,
    },
    {
      header: 'Read result',
      cell: (t) => (
        <div className='max-w-md text-xs whitespace-normal'>
          <p>
            {t.usersComplete
              ? 'Users complete'
              : t.usersError || 'Users incomplete'}
          </p>
          {t.groupsError && (
            <p className='mt-1 text-destructive'>{t.groupsError}</p>
          )}
        </div>
      ),
    },
    {
      header: 'Connection',
      cell: (t) => (
        <Link
          to='/connections/$connectionId'
          params={{ connectionId: t.connectionId }}
          className='text-primary underline'
        >
          Open
        </Link>
      ),
    },
  ]
  const users: Column<WorkspaceOverviewUser>[] = [
    {
      header: 'User',
      cell: (r) => (
        <Button variant='link' className='px-0' asChild>
          <Link
            to='/connections/$connectionId/tenants/$tenantId/users/$userId'
            params={{
              connectionId: r.connectionId,
              tenantId: r.tenantId,
              userId: r.user?.id ?? '',
            }}
          >
            {r.user?.primaryEmail}
          </Link>
        </Button>
      ),
    },
    {
      header: 'Tenant',
      cell: (r) => (
        <div>
          <p>{r.tenantName}</p>
          <p className='text-xs text-muted-foreground'>{r.connectionName}</p>
        </div>
      ),
    },
    {
      header: 'Role',
      cell: (r) =>
        r.user?.superAdmin === true
          ? 'Super admin'
          : r.user?.delegatedAdmin === true
            ? 'Delegated admin'
            : r.user?.superAdmin === undefined ||
                r.user?.delegatedAdmin === undefined
              ? 'Unknown'
              : 'User',
    },
    {
      header: '2-step enrolled / enforced',
      cell: (r) =>
        `${triState(r.user?.twoStepEnrolled)} / ${triState(r.user?.twoStepEnforced)}`,
    },
    {
      header: 'Last login',
      cell: (r) => lastLoginText(r.user?.lastLoginTime ?? ''),
    },
    {
      header: 'Findings',
      cell: (r) => (
        <div className='flex flex-wrap gap-1'>
          {r.adminWithoutTwoStep && (
            <Badge variant='destructive'>Admin without 2-step</Badge>
          )}
          {r.inactive && <Badge variant='outline'>Inactive</Badge>}
          {r.neverLoggedIn && <Badge variant='outline'>Never logged in</Badge>}
          {r.securityUnknown && <Badge variant='outline'>Unknown data</Badge>}
          {r.user?.suspended && <Badge variant='outline'>Suspended</Badge>}
        </div>
      ),
    },
  ]
  return (
    <section className='space-y-5'>
      <div className='flex items-center justify-between gap-3'>
        <div>
          <h2 className='text-xl font-semibold'>
            {securityOnly ? 'Account security' : 'Workspace overview'}
          </h2>
          <p className='text-xs text-muted-foreground'>
            Observed {formatWorkspaceTime(data.data?.observedAt)} · counts are
            deduplicated by tenant Customer ID and user ID.
          </p>
        </div>
        <Button
          variant='outline'
          disabled={data.isFetching}
          onClick={() => void data.refetch()}
        >
          Refresh
        </Button>
      </div>
      <p className='text-sm text-muted-foreground'>
        Read on demand. Completeness means all returned pages were read;
        visibility still depends on Google's permissions. Missing fields are
        unknown, not disabled. Inactive means no login within {days} days; a
        never-used account is inactive only when its known creation date is also
        older than {days} days.
      </p>
      <DirectoryError error={data.error} />
      {data.data && !data.data.usersComplete && (
        <Alert>
          <AlertDescription>
            Some tenants could not be read completely. Counts and search results
            cover only the accounts observed; see each tenant's read result
            below.
          </AlertDescription>
        </Alert>
      )}
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-3'>
        {cards.map(([label, value]) => (
          <div key={label} className='rounded-lg border bg-card p-4'>
            <p className='text-sm text-muted-foreground'>{label}</p>
            <p className='mt-1 text-2xl font-semibold'>
              {value === undefined ? '—' : String(value)}
              {data.data && !data.data.usersComplete && (
                <span className='ms-2 text-xs font-normal text-muted-foreground'>
                  observed
                </span>
              )}
            </p>
          </div>
        ))}
      </div>
      <div className='flex flex-wrap items-end gap-3'>
        <form
          className='flex min-w-48 flex-1 gap-2'
          onSubmit={(e) => {
            e.preventDefault()
            setQuery(search.trim())
            setPage(1)
          }}
        >
          <Input
            aria-label='Search Workspace accounts'
            placeholder='Email, name, organization or tenant'
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <Button variant='outline'>Search</Button>
        </form>
        <select
          aria-label='Security filter'
          className='h-9 rounded-md border bg-background px-3 text-sm'
          value={filter}
          onChange={(e) => {
            setFilter(e.target.value)
            setPage(1)
          }}
        >
          {filters.map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
        <div className='space-y-2'>
          <Label htmlFor={`inactive-days-${tenantId || connectionId || 'all'}`}>
            Inactive days
          </Label>
          <Input
            id={`inactive-days-${tenantId || connectionId || 'all'}`}
            type='number'
            min={1}
            max={3650}
            className='w-24'
            value={days}
            onChange={(e) => {
              const n = Number(e.target.value)
              if (n >= 1 && n <= 3650) {
                setDays(n)
                setPage(1)
              }
            }}
          />
        </div>
        {!securityOnly && (
          <label className='flex items-center gap-2 text-sm'>
            <input
              type='checkbox'
              checked={groups}
              onChange={(e) => setGroups(e.target.checked)}
            />
            Read group counts (optional permission)
          </label>
        )}
      </div>
      {!data.error && (
        <>
          <h3 className='font-semibold'>Tenant reads</h3>
          <DataTable
            columns={tenants}
            data={data.data?.tenants}
            isLoading={data.isLoading}
            emptyMessage='No Workspace tenants configured'
          />
          <h3 className='font-semibold'>Accounts</h3>
          <DataTable
            columns={users}
            data={data.data?.users}
            isLoading={data.isLoading}
            emptyMessage='No observed accounts match this filter'
          />
        </>
      )}
      <div className='flex items-center justify-end gap-3'>
        <span className='text-sm text-muted-foreground'>
          {Number(data.data?.totalCount ?? 0)} matching observed accounts · page{' '}
          {page}
        </span>
        <Button
          variant='outline'
          disabled={page === 1 || data.isFetching}
          onClick={() => setPage((p) => p - 1)}
        >
          Previous
        </Button>
        <Button
          variant='outline'
          disabled={
            !!data.error ||
            data.isFetching ||
            page * 50 >= Number(data.data?.totalCount ?? 0)
          }
          onClick={() => setPage((p) => p + 1)}
        >
          Next
        </Button>
      </div>
    </section>
  )
}
