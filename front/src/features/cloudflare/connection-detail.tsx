import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ChevronLeft, TriangleAlert } from 'lucide-react'
import {
  CloudflareRecordAccess,
  useCloudflarePagesProjects,
  useCloudflareWorkers,
  useCloudflareZones,
  type CloudflareAddressIssue,
  type CloudflarePagesProject,
  type CloudflareWorker,
  type CloudflareZone,
  type ListQuery,
} from '@/api/cloudflare'
import { type Connection } from '@/api/connections'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { DataTable, type Column } from '@/components/data-table'
import { AddressList } from './address-list'
import { cloudflareConfig } from './format'
import {
  ListFrame,
  Pager,
  ProblemAlert,
  RefreshButton,
  SearchBox,
} from './list-parts'

const PAGE_SIZE = 20

function useListQuery() {
  const [q, setQ] = useState<ListQuery>({
    query: '',
    page: 1,
    pageSize: PAGE_SIZE,
  })
  return {
    q,
    setQuery: (query: string) => setQ((v) => ({ ...v, query, page: 1 })),
    setPage: (page: number) => setQ((v) => ({ ...v, page })),
  }
}

/** A Cloudflare connection: the Zones, Workers and Pages its token sees. */
export function CloudflareConnectionDetail({
  connection,
}: {
  connection: Connection
}) {
  return (
    <Page>
      <PageHeader
        breadcrumb={
          <Link
            to='/connections'
            search={{ provider: 'cloudflare' }}
            className='inline-flex items-center gap-1'
          >
            <ChevronLeft className='h-3 w-3' /> Cloudflare connections
          </Link>
        }
        title={connection.name}
        subtitle={
          <span>
            Account{' '}
            <span className='font-mono'>
              {cloudflareConfig(connection)?.accountId}
            </span>{' '}
            · showing what this API token can see, read live from Cloudflare
          </span>
        }
        actions={<RefreshButton connectionId={connection.id} />}
      />
      <PageScroll>
        <Tabs defaultValue='zones'>
          <TabsList>
            <TabsTrigger value='zones'>Zones</TabsTrigger>
            <TabsTrigger value='workers'>Workers</TabsTrigger>
            <TabsTrigger value='pages'>Pages</TabsTrigger>
          </TabsList>
          <TabsContent value='zones' className='pt-2'>
            <ZonesTab connectionId={connection.id} />
          </TabsContent>
          <TabsContent value='workers' className='pt-2'>
            <WorkersTab connectionId={connection.id} />
          </TabsContent>
          <TabsContent value='pages' className='pt-2'>
            <PagesTab connectionId={connection.id} />
          </TabsContent>
        </Tabs>
      </PageScroll>
    </Page>
  )
}

function ZonesTab({ connectionId }: { connectionId: string }) {
  const { q, setQuery, setPage } = useListQuery()
  const zones = useCloudflareZones(connectionId, q)

  const columns: Column<CloudflareZone>[] = [
    {
      header: 'Zone',
      cell: (z) => (
        <Link
          to='/connections/$connectionId/zones/$zoneId'
          params={{ connectionId, zoneId: z.id }}
          className='font-medium hover:underline'
        >
          {z.name}
        </Link>
      ),
    },
    {
      header: 'Status',
      cell: (z) => (
        <div className='flex gap-1'>
          <Badge variant={z.status === 'active' ? 'secondary' : 'outline'}>
            {z.status}
          </Badge>
          {z.paused && <Badge variant='outline'>paused</Badge>}
        </div>
      ),
    },
    { header: 'Type', cell: (z) => z.type || '-' },
    {
      header: 'DNS',
      cell: (z) =>
        z.dnsAccess === CloudflareRecordAccess.READ ? (
          <Badge variant='outline'>Read-only</Badge>
        ) : (
          <span className='text-xs text-muted-foreground'>Edit unknown</span>
        ),
    },
  ]

  return (
    <ListFrame
      search={
        <SearchBox
          value={q.query}
          onChange={setQuery}
          placeholder='Search zones'
        />
      }
      pager={
        <Pager
          info={zones.data?.pageInfo}
          onPage={setPage}
          busy={zones.isFetching}
        />
      }
    >
      {zones.error ? (
        <ProblemAlert error={zones.error} permission='Zone · Read' />
      ) : (
        <DataTable
          columns={columns}
          data={zones.data?.zones}
          isLoading={zones.isLoading}
          emptyMessage={
            q.query ? 'No zones match' : 'No zones visible to this token'
          }
          emptyDescription={
            q.query
              ? undefined
              : 'The token sees no zones in this account. If you expect some, check that its zone resources include them.'
          }
        />
      )}
    </ListFrame>
  )
}

function IssuesAlert({
  issues,
}: {
  issues: CloudflareAddressIssue[] | undefined
}) {
  if (!issues?.length) return null
  return (
    <Alert>
      <TriangleAlert />
      <AlertTitle>Some addresses could not be read</AlertTitle>
      <AlertDescription>
        <ul className='list-disc ps-4'>
          {issues.map((i) => (
            <li key={i.source}>
              {i.source}: {i.message}
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  )
}

function WorkersTab({ connectionId }: { connectionId: string }) {
  const { q, setQuery, setPage } = useListQuery()
  const workers = useCloudflareWorkers(connectionId, q)

  const columns: Column<CloudflareWorker>[] = [
    {
      header: 'Worker',
      cell: (w) => <span className='font-medium'>{w.name}</span>,
    },
    {
      header: 'Addresses',
      cell: (w) => (
        <AddressList
          addresses={w.addresses}
          incomplete={w.addressesIncomplete}
        />
      ),
    },
  ]

  return (
    <ListFrame
      search={
        <SearchBox
          value={q.query}
          onChange={setQuery}
          placeholder='Search Workers'
        />
      }
      pager={
        <Pager
          info={workers.data?.pageInfo}
          onPage={setPage}
          busy={workers.isFetching}
        />
      }
    >
      {workers.error ? (
        <ProblemAlert
          error={workers.error}
          permission='Workers Scripts · Read'
        />
      ) : (
        <>
          <IssuesAlert issues={workers.data?.addressIssues} />
          <DataTable
            columns={columns}
            data={workers.data?.workers}
            isLoading={workers.isLoading}
            emptyMessage={
              q.query ? 'No Workers match' : 'No Workers visible to this token'
            }
          />
        </>
      )}
    </ListFrame>
  )
}

function PagesTab({ connectionId }: { connectionId: string }) {
  const { q, setQuery, setPage } = useListQuery()
  const projects = useCloudflarePagesProjects(connectionId, q)

  const columns: Column<CloudflarePagesProject>[] = [
    {
      header: 'Project',
      cell: (p) => <span className='font-medium'>{p.name}</span>,
    },
    {
      header: 'Addresses',
      cell: (p) => (
        <AddressList
          addresses={p.addresses}
          incomplete={p.addressesIncomplete}
        />
      ),
    },
  ]

  return (
    <ListFrame
      search={
        <SearchBox
          value={q.query}
          onChange={setQuery}
          placeholder='Search projects'
        />
      }
      pager={
        <Pager
          info={projects.data?.pageInfo}
          onPage={setPage}
          busy={projects.isFetching}
        />
      }
    >
      {projects.error ? (
        <ProblemAlert
          error={projects.error}
          permission='Cloudflare Pages · Read'
        />
      ) : (
        <>
          <IssuesAlert issues={projects.data?.addressIssues} />
          <DataTable
            columns={columns}
            data={projects.data?.projects}
            isLoading={projects.isLoading}
            emptyMessage={
              q.query
                ? 'No projects match'
                : 'No Pages projects visible to this token'
            }
          />
        </>
      )}
    </ListFrame>
  )
}
