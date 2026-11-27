import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ChevronLeft, Globe, Lock, ShieldCheck, ShieldOff } from 'lucide-react'
import { type Connection } from '@/api/connections'
import {
  PorkbunDomainFilter,
  usePorkbunDomains,
  type DomainListQuery,
  type PorkbunDomain,
  type PorkbunPortfolioSummary,
} from '@/api/porkbun'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { DataTable, type Column } from '@/components/data-table'
import { expiryStatus, maskApiKey, porkbunConfig } from './format'
import { Pager, ProblemAlert, RefreshButton, SearchBox } from './list-parts'

const PAGE_SIZE = 20

const FILTER_OPTIONS: { value: PorkbunDomainFilter; label: string }[] = [
  { value: PorkbunDomainFilter.UNSPECIFIED, label: 'All' },
  { value: PorkbunDomainFilter.EXPIRING_SOON, label: 'Expiring ≤ 30d' },
  { value: PorkbunDomainFilter.AUTO_RENEW_OFF, label: 'Auto-renew off' },
  { value: PorkbunDomainFilter.API_ACCESS_OFF, label: 'API access off' },
  { value: PorkbunDomainFilter.EXTERNAL_NAMESERVERS, label: 'External NS' },
]

/** A Porkbun connection: live read-only view of the account's domains. */
export function PorkbunConnectionDetail({
  connection,
}: {
  connection: Connection
}) {
  const cfg = porkbunConfig(connection)
  const [q, setQ] = useState<DomainListQuery>({
    query: '',
    filter: PorkbunDomainFilter.UNSPECIFIED,
    page: 1,
    pageSize: PAGE_SIZE,
  })

  const domains = usePorkbunDomains(connection.id, q)

  const setFilter = (filter: PorkbunDomainFilter) =>
    setQ((prev) => ({
      ...prev,
      filter:
        prev.filter === filter && filter !== PorkbunDomainFilter.UNSPECIFIED
          ? PorkbunDomainFilter.UNSPECIFIED
          : filter,
      page: 1,
    }))

  const columns: Column<PorkbunDomain>[] = [
    {
      header: 'Domain',
      cell: (d) => (
        <div className='space-y-1'>
          <Link
            to='/connections/$connectionId/domains/$domain'
            params={{ connectionId: connection.id, domain: d.domain }}
            className='font-medium hover:underline'
          >
            {d.domain}
          </Link>
          {d.labels.length > 0 && (
            <div className='flex flex-wrap gap-1'>
              {d.labels.map((l) => (
                <Badge
                  key={l.id || l.title}
                  variant='outline'
                  className='h-5 gap-1 px-1.5 text-[11px] font-normal'
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
            </div>
          )}
        </div>
      ),
    },
    {
      header: 'Status',
      cell: (d) => (
        <Badge
          variant={
            d.status.toUpperCase() === 'ACTIVE' ? 'secondary' : 'outline'
          }
        >
          {d.status || '-'}
        </Badge>
      ),
    },
    {
      header: 'Expires',
      cell: (d) => {
        const exp = expiryStatus(d.expiresAt)
        return (
          <div className='space-y-0.5'>
            <div className='font-mono text-xs'>{exp.dateText}</div>
            {exp.tone !== 'none' && (
              <Badge
                variant={
                  exp.tone === 'expired'
                    ? 'destructive'
                    : exp.tone === 'soon'
                      ? 'default'
                      : 'outline'
                }
                className='h-5 text-[11px] font-normal'
              >
                {exp.label}
              </Badge>
            )}
          </div>
        )
      },
    },
    {
      header: 'Auto-renew',
      cell: (d) =>
        d.autoRenew ? (
          <Badge variant='secondary'>On</Badge>
        ) : (
          <Badge variant='outline'>Off</Badge>
        ),
    },
    {
      header: 'Protection',
      cell: (d) => (
        <div className='flex flex-wrap gap-1'>
          <Badge
            variant={d.securityLock ? 'secondary' : 'outline'}
            className='gap-1'
            title={
              d.securityLock
                ? 'Transfer lock enabled'
                : 'Transfer lock disabled'
            }
          >
            <Lock className='h-3 w-3' />
            {d.securityLock ? 'Locked' : 'Unlocked'}
          </Badge>
          <Badge
            variant={d.whoisPrivacy ? 'secondary' : 'outline'}
            className='gap-1'
            title={
              d.whoisPrivacy
                ? 'WHOIS privacy enabled'
                : 'WHOIS privacy disabled'
            }
          >
            {d.whoisPrivacy ? (
              <ShieldCheck className='h-3 w-3' />
            ) : (
              <ShieldOff className='h-3 w-3' />
            )}
            WHOIS
          </Badge>
        </div>
      ),
    },
    {
      header: 'API & DNS',
      cell: (d) => (
        <div className='flex flex-wrap gap-1'>
          <Badge variant={d.apiAccess ? 'secondary' : 'outline'}>
            {d.apiAccess ? 'API on' : 'API off'}
          </Badge>
          {d.notLocal && (
            <Badge
              variant='outline'
              className='gap-1'
              title='Domain is flagged as using external nameservers or hosting'
            >
              <Globe className='h-3 w-3' />
              External NS
            </Badge>
          )}
        </div>
      ),
    },
  ]

  return (
    <Page>
      <PageHeader
        breadcrumb={
          <Link
            to='/connections'
            search={{ provider: 'porkbun' }}
            className='inline-flex items-center gap-1'
          >
            <ChevronLeft className='h-3 w-3' /> Porkbun connections
          </Link>
        }
        title={
          <span className='inline-flex items-center gap-2'>
            {connection.name}
            {cfg?.isSandbox && <Badge variant='outline'>Sandbox</Badge>}
          </span>
        }
        subtitle={
          <span>
            API key{' '}
            <span className='font-mono'>
              {cfg?.apiKey ? maskApiKey(cfg.apiKey) : '-'}
            </span>{' '}
            · read-only live view from Porkbun
          </span>
        }
        actions={<RefreshButton connectionId={connection.id} />}
      />
      <PageScroll>
        <div className='space-y-4'>
          {domains.data?.summary && (
            <SummaryCards
              summary={domains.data.summary}
              activeFilter={q.filter}
              onSelectFilter={setFilter}
            />
          )}

          {domains.isError ? (
            <ProblemAlert error={domains.error} />
          ) : (
            <div className='space-y-4'>
              <div className='flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between'>
                <SearchBox
                  value={q.query}
                  onChange={(query) =>
                    setQ((prev) => ({ ...prev, query, page: 1 }))
                  }
                  placeholder='Search domains, TLDs, or labels'
                />
                <div className='flex flex-wrap gap-1.5'>
                  {FILTER_OPTIONS.map((opt) => (
                    <Button
                      key={opt.value}
                      size='sm'
                      variant={q.filter === opt.value ? 'default' : 'outline'}
                      onClick={() =>
                        setQ((prev) => ({
                          ...prev,
                          filter: opt.value,
                          page: 1,
                        }))
                      }
                    >
                      {opt.label}
                    </Button>
                  ))}
                </div>
              </div>

              <DataTable
                columns={columns}
                data={domains.data?.domains}
                isLoading={domains.isLoading}
                emptyMessage={
                  q.query || q.filter !== PorkbunDomainFilter.UNSPECIFIED
                    ? 'No domains match this search or filter.'
                    : 'No domains found in this Porkbun account.'
                }
              />

              <Pager
                info={domains.data?.pageInfo}
                onPage={(page) => setQ((prev) => ({ ...prev, page }))}
                busy={domains.isFetching}
              />
            </div>
          )}
        </div>
      </PageScroll>
    </Page>
  )
}

function SummaryCards({
  summary,
  activeFilter,
  onSelectFilter,
}: {
  summary: PorkbunPortfolioSummary
  activeFilter: PorkbunDomainFilter
  onSelectFilter: (filter: PorkbunDomainFilter) => void
}) {
  const cards: {
    label: string
    value: number
    sub?: string
    filter: PorkbunDomainFilter
    highlight?: boolean
  }[] = [
    {
      label: 'Total domains',
      value: summary.totalDomains,
      filter: PorkbunDomainFilter.UNSPECIFIED,
    },
    {
      label: 'Expiring ≤ 30d',
      value: summary.expiringSoonCount,
      sub:
        summary.expiredCount > 0
          ? `${summary.expiredCount} already expired`
          : undefined,
      filter: PorkbunDomainFilter.EXPIRING_SOON,
      highlight: summary.expiringSoonCount > 0,
    },
    {
      label: 'Auto-renew off',
      value: summary.autoRenewOffCount,
      filter: PorkbunDomainFilter.AUTO_RENEW_OFF,
    },
    {
      label: 'API access off',
      value: summary.apiAccessOffCount,
      filter: PorkbunDomainFilter.API_ACCESS_OFF,
    },
    {
      label: 'External NS',
      value: summary.externalNameserversCount,
      filter: PorkbunDomainFilter.EXTERNAL_NAMESERVERS,
    },
  ]

  return (
    <div className='grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5'>
      {cards.map((c) => {
        const active = activeFilter === c.filter
        return (
          <Card
            key={c.label}
            className={cn(
              'cursor-pointer transition-colors hover:border-foreground/30',
              active && 'border-primary ring-1 ring-primary/30'
            )}
            onClick={() => onSelectFilter(c.filter)}
          >
            <CardContent className='p-4'>
              <div className='text-xs text-muted-foreground'>{c.label}</div>
              <div
                className={cn(
                  'mt-1 text-2xl font-semibold tabular-nums',
                  c.highlight && 'text-amber-600 dark:text-amber-400'
                )}
              >
                {c.value.toLocaleString()}
              </div>
              {c.sub && (
                <div className='mt-0.5 text-xs text-destructive'>{c.sub}</div>
              )}
            </CardContent>
          </Card>
        )
      })}
    </div>
  )
}
