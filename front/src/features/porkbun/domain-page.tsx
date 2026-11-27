import { useState } from 'react'
import { getRouteApi, Link } from '@tanstack/react-router'
import {
  ChevronLeft,
  Globe,
  Info,
  Lock,
  ShieldCheck,
  ShieldOff,
  TriangleAlert,
} from 'lucide-react'
import { useConnection } from '@/api/connections'
import {
  usePorkbunDNSRecords,
  usePorkbunDNSSECRecords,
  usePorkbunDomain,
  usePorkbunGlueRecords,
  usePorkbunURLForwards,
  type PorkbunDNSRecord,
  type PorkbunDNSSECRecord,
  type PorkbunGlueHost,
  type PorkbunURLForward,
  type RecordListQuery,
} from '@/api/porkbun'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { DataTable, type Column } from '@/components/data-table'
import {
  API_ACCESS_DOCS,
  expiryStatus,
  formatDnssecAlgorithm,
  formatDnssecDigestType,
  formatTTL,
  formatUtcDate,
} from './format'
import {
  CopyValueButton,
  Pager,
  ProblemAlert,
  RefreshButton,
  SearchBox,
} from './list-parts'

const route = getRouteApi(
  '/_authenticated/connections/$connectionId/domains/$domain'
)

const ALL_TYPES = 'all'
const DNS_PAGE_SIZE = 50

type DomainTab = 'dns' | 'forwards' | 'dnssec' | 'glue'

/** Read-only detail page for one Porkbun domain. */
export function PorkbunDomainPage() {
  const { connectionId, domain } = route.useParams()
  const connection = useConnection(connectionId)
  const detail = usePorkbunDomain(connectionId, domain)
  const [tab, setTab] = useState<DomainTab>('dns')

  const dom = detail.data?.domain
  const exp = expiryStatus(dom?.expiresAt)
  const nameservers = detail.data?.nameservers ?? []
  const usesPorkbunNS = detail.data?.usesPorkbunNameservers ?? false
  const nameserversIssue = detail.data?.nameserversIssue ?? ''

  return (
    <Page>
      <PageHeader
        breadcrumb={
          <Link
            to='/connections/$connectionId'
            params={{ connectionId }}
            className='inline-flex items-center gap-1'
          >
            <ChevronLeft className='h-3 w-3' />{' '}
            {connection.data?.name ?? 'Connection'}
          </Link>
        }
        title={
          dom?.domain ??
          (detail.isLoading ? <Skeleton className='h-8 w-56' /> : domain)
        }
        subtitle={
          dom && (
            <span className='inline-flex flex-wrap items-center gap-2'>
              <Badge
                variant={
                  dom.status.toUpperCase() === 'ACTIVE'
                    ? 'secondary'
                    : 'outline'
                }
              >
                {dom.status || 'UNKNOWN'}
              </Badge>
              <span className='text-xs'>
                Created {formatUtcDate(dom.createdAt)} · Expires {exp.dateText}
                {exp.tone !== 'none' ? ` (${exp.label})` : ''}
              </span>
            </span>
          )
        }
        actions={<RefreshButton connectionId={connectionId} />}
      />
      <PageScroll>
        <div className='space-y-4'>
          {detail.isError ? (
            <ProblemAlert error={detail.error} />
          ) : (
            <>
              {dom && (
                <div className='flex flex-wrap items-center gap-2 rounded-lg border bg-card p-4 text-sm'>
                  <Badge variant={dom.autoRenew ? 'secondary' : 'outline'}>
                    Auto-renew: {dom.autoRenew ? 'On' : 'Off'}
                  </Badge>
                  <Badge
                    variant={dom.securityLock ? 'secondary' : 'outline'}
                    className='gap-1'
                  >
                    <Lock className='h-3 w-3' />
                    {dom.securityLock ? 'Transfer locked' : 'Unlocked'}
                  </Badge>
                  <Badge
                    variant={dom.whoisPrivacy ? 'secondary' : 'outline'}
                    className='gap-1'
                  >
                    {dom.whoisPrivacy ? (
                      <ShieldCheck className='h-3 w-3' />
                    ) : (
                      <ShieldOff className='h-3 w-3' />
                    )}
                    WHOIS privacy: {dom.whoisPrivacy ? 'On' : 'Off'}
                  </Badge>
                  <Badge variant={dom.apiAccess ? 'secondary' : 'outline'}>
                    API access: {dom.apiAccess ? 'Enabled' : 'Disabled'}
                  </Badge>
                  {dom.labels.map((l) => (
                    <Badge
                      key={l.id || l.title}
                      variant='outline'
                      className='gap-1'
                    >
                      {l.color && (
                        <span
                          className='inline-block h-2 w-2 rounded-full'
                          style={{ backgroundColor: l.color }}
                        />
                      )}
                      {l.title}
                    </Badge>
                  ))}

                  <div className='mt-2 flex w-full flex-wrap items-center gap-2 border-t pt-3 text-xs'>
                    <span className='font-medium text-muted-foreground'>
                      Nameservers:
                    </span>
                    {nameservers.length > 0 ? (
                      <>
                        <Badge
                          variant={usesPorkbunNS ? 'secondary' : 'outline'}
                          className='gap-1'
                        >
                          <Globe className='h-3 w-3' />
                          {usesPorkbunNS ? 'Porkbun DNS' : 'External NS'}
                        </Badge>
                        <span className='font-mono text-muted-foreground'>
                          {nameservers.join(', ')}
                        </span>
                      </>
                    ) : nameserversIssue ? (
                      <span className='text-muted-foreground'>
                        Unavailable ({nameserversIssue})
                      </span>
                    ) : (
                      <span className='text-muted-foreground'>None listed</span>
                    )}
                  </div>
                </div>
              )}

              {dom && !dom.apiAccess && (
                <Alert>
                  <Info />
                  <AlertTitle>
                    API access is disabled for this domain
                  </AlertTitle>
                  <AlertDescription>
                    Porkbun requires opting each domain into API access before
                    nameservers, DNS records, URL forwards, DNSSEC or glue
                    records can be read. Enable{' '}
                    <span className='font-medium'>API Access</span> under this
                    domain in your{' '}
                    <a
                      href={API_ACCESS_DOCS}
                      target='_blank'
                      rel='noreferrer'
                      className='underline underline-offset-2'
                    >
                      Porkbun dashboard
                    </a>{' '}
                    and click Refresh.
                  </AlertDescription>
                </Alert>
              )}

              <Tabs
                value={tab}
                onValueChange={(v) => setTab(v as DomainTab)}
                className='space-y-3'
              >
                <TabsList>
                  <TabsTrigger value='dns'>DNS records</TabsTrigger>
                  <TabsTrigger value='forwards'>URL forwards</TabsTrigger>
                  <TabsTrigger value='dnssec'>DNSSEC</TabsTrigger>
                  <TabsTrigger value='glue'>Glue records</TabsTrigger>
                </TabsList>

                <TabsContent value='dns' className='space-y-3 pt-1'>
                  {nameservers.length > 0 && !usesPorkbunNS && (
                    <Alert>
                      <TriangleAlert />
                      <AlertTitle>
                        External nameservers are active for {domain}
                      </AlertTitle>
                      <AlertDescription>
                        This domain delegates DNS to{' '}
                        <span className='font-mono'>
                          {nameservers.join(', ')}
                        </span>
                        . The records listed below are stored at Porkbun and are
                        not currently authoritative on the public internet.
                      </AlertDescription>
                    </Alert>
                  )}
                  <DNSRecordsTab
                    connectionId={connectionId}
                    domain={domain}
                    active={tab === 'dns'}
                  />
                </TabsContent>

                <TabsContent value='forwards' className='pt-1'>
                  <URLForwardsTab
                    connectionId={connectionId}
                    domain={domain}
                    active={tab === 'forwards'}
                  />
                </TabsContent>

                <TabsContent value='dnssec' className='pt-1'>
                  <DNSSECTab
                    connectionId={connectionId}
                    domain={domain}
                    active={tab === 'dnssec'}
                  />
                </TabsContent>

                <TabsContent value='glue' className='pt-1'>
                  <GlueTab
                    connectionId={connectionId}
                    domain={domain}
                    active={tab === 'glue'}
                  />
                </TabsContent>
              </Tabs>
            </>
          )}
        </div>
      </PageScroll>
    </Page>
  )
}

function DNSRecordsTab({
  connectionId,
  domain,
  active,
}: {
  connectionId: string
  domain: string
  active: boolean
}) {
  const [q, setQ] = useState<RecordListQuery>({
    query: '',
    type: '',
    page: 1,
    pageSize: DNS_PAGE_SIZE,
  })
  const records = usePorkbunDNSRecords(connectionId, domain, q, active)

  if (records.isError) {
    return <ProblemAlert error={records.error} />
  }

  const recordTypes = records.data?.recordTypes ?? []

  const columns: Column<PorkbunDNSRecord>[] = [
    {
      header: 'Type',
      cell: (r) => (
        <Badge variant='secondary' className='font-mono'>
          {r.type}
        </Badge>
      ),
    },
    {
      header: 'Name',
      cell: (r) => (
        <span className='font-mono text-xs break-all'>{r.name || domain}</span>
      ),
    },
    {
      header: 'Content',
      cell: (r) => (
        <div className='flex max-w-md items-start gap-1.5'>
          <span className='font-mono text-xs break-all'>{r.content}</span>
          <CopyValueButton value={r.content} label='Copy record content' />
        </div>
      ),
    },
    {
      header: 'Priority',
      cell: (r) => (
        <span className='font-mono text-xs text-muted-foreground'>
          {r.priority && r.priority !== '0' ? r.priority : '-'}
        </span>
      ),
    },
    {
      header: 'TTL',
      cell: (r) => (
        <span className='font-mono text-xs text-muted-foreground'>
          {formatTTL(r.ttl)}
        </span>
      ),
    },
    {
      header: 'Notes',
      cell: (r) => (
        <span className='text-xs text-muted-foreground'>{r.notes || '-'}</span>
      ),
    },
  ]

  return (
    <div className='space-y-4'>
      <div className='flex flex-col gap-2 sm:flex-row sm:items-center'>
        <SearchBox
          value={q.query}
          onChange={(query) => setQ((prev) => ({ ...prev, query, page: 1 }))}
          placeholder='Search name, content, or notes'
        />
        <Select
          value={q.type || ALL_TYPES}
          onValueChange={(v) =>
            setQ((prev) => ({
              ...prev,
              type: v === ALL_TYPES ? '' : v,
              page: 1,
            }))
          }
        >
          <SelectTrigger className='w-full sm:w-40' aria-label='Record type'>
            <SelectValue placeholder='All types' />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_TYPES}>All types</SelectItem>
            {recordTypes.map((t) => (
              <SelectItem key={t} value={t}>
                {t}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <DataTable
        columns={columns}
        data={records.data?.records}
        isLoading={records.isLoading}
        emptyMessage={
          q.query || q.type
            ? 'No DNS records match this filter.'
            : 'No DNS records found for this domain.'
        }
      />

      <Pager
        info={records.data?.pageInfo}
        onPage={(page) => setQ((prev) => ({ ...prev, page }))}
        busy={records.isFetching}
      />
    </div>
  )
}

function URLForwardsTab({
  connectionId,
  domain,
  active,
}: {
  connectionId: string
  domain: string
  active: boolean
}) {
  const forwards = usePorkbunURLForwards(connectionId, domain, active)
  if (forwards.isError) return <ProblemAlert error={forwards.error} />

  const columns: Column<PorkbunURLForward>[] = [
    {
      header: 'Host',
      cell: (f) => (
        <span className='font-mono text-xs'>
          {f.subdomain ? `${f.subdomain}.${domain}` : domain}
        </span>
      ),
    },
    {
      header: 'Destination',
      cell: (f) => (
        <div className='flex max-w-md items-center gap-1.5'>
          <span className='font-mono text-xs break-all'>{f.location}</span>
          <CopyValueButton value={f.location} label='Copy destination URL' />
        </div>
      ),
    },
    {
      header: 'Type',
      cell: (f) => <Badge variant='secondary'>{f.type || '-'}</Badge>,
    },
    {
      header: 'Options',
      cell: (f) => (
        <div className='flex flex-wrap gap-1'>
          {f.includePath && <Badge variant='outline'>Include path</Badge>}
          {f.wildcard && <Badge variant='outline'>Wildcard</Badge>}
          {!f.includePath && !f.wildcard && (
            <span className='text-xs text-muted-foreground'>-</span>
          )}
        </div>
      ),
    },
  ]

  return (
    <DataTable
      columns={columns}
      data={forwards.data}
      isLoading={forwards.isLoading}
      emptyMessage='No URL forwarding rules configured for this domain.'
    />
  )
}

function DNSSECTab({
  connectionId,
  domain,
  active,
}: {
  connectionId: string
  domain: string
  active: boolean
}) {
  const ds = usePorkbunDNSSECRecords(connectionId, domain, active)
  if (ds.isError) return <ProblemAlert error={ds.error} />

  const columns: Column<PorkbunDNSSECRecord>[] = [
    {
      header: 'Key tag',
      cell: (r) => <span className='font-mono text-xs'>{r.keyTag}</span>,
    },
    {
      header: 'Algorithm',
      cell: (r) => (
        <span className='text-xs'>{formatDnssecAlgorithm(r.algorithm)}</span>
      ),
    },
    {
      header: 'Digest type',
      cell: (r) => (
        <span className='text-xs'>{formatDnssecDigestType(r.digestType)}</span>
      ),
    },
    {
      header: 'Digest',
      cell: (r) => (
        <div className='flex max-w-lg items-start gap-1.5'>
          <span className='font-mono text-xs break-all'>{r.digest}</span>
          <CopyValueButton value={r.digest} label='Copy DS digest' />
        </div>
      ),
    },
  ]

  return (
    <DataTable
      columns={columns}
      data={ds.data}
      isLoading={ds.isLoading}
      emptyMessage='No DNSSEC DS records configured for this domain.'
    />
  )
}

function GlueTab({
  connectionId,
  domain,
  active,
}: {
  connectionId: string
  domain: string
  active: boolean
}) {
  const glue = usePorkbunGlueRecords(connectionId, domain, active)
  if (glue.isError) return <ProblemAlert error={glue.error} />

  const columns: Column<PorkbunGlueHost>[] = [
    {
      header: 'Host',
      cell: (h) => <span className='font-mono text-xs'>{h.host}</span>,
    },
    {
      header: 'IPv4',
      cell: (h) =>
        h.ipv4.length > 0 ? (
          <div className='flex flex-wrap gap-1'>
            {h.ipv4.map((ip) => (
              <Badge key={ip} variant='outline' className='font-mono text-xs'>
                {ip}
              </Badge>
            ))}
          </div>
        ) : (
          <span className='text-xs text-muted-foreground'>-</span>
        ),
    },
    {
      header: 'IPv6',
      cell: (h) =>
        h.ipv6.length > 0 ? (
          <div className='flex flex-wrap gap-1'>
            {h.ipv6.map((ip) => (
              <Badge key={ip} variant='outline' className='font-mono text-xs'>
                {ip}
              </Badge>
            ))}
          </div>
        ) : (
          <span className='text-xs text-muted-foreground'>-</span>
        ),
    },
  ]

  return (
    <DataTable
      columns={columns}
      data={glue.data}
      isLoading={glue.isLoading}
      emptyMessage='No glue records configured for this domain.'
    />
  )
}
