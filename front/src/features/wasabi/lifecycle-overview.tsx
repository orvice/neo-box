import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { Search, TriangleAlert } from 'lucide-react'
import { useWasabiBuckets } from '@/api/wasabi'
import { cn } from '@/lib/utils'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  LifecycleRuleNameCell,
  LifecycleRuleScopeCell,
} from './bucket-settings'
import {
  collectAccountLifecycle,
  filterAccountLifecycle,
  formatAbortMultipartAction,
  formatCurrentVersionAction,
  formatNoncurrentVersionAction,
  WASABI_MIN_STORAGE_DAYS,
  type LifecycleFilterStatus,
} from './lifecycle'

/**
 * Cross-bucket read-only summary of lifecycle rules for a Wasabi connection.
 * Reuses the active buckets query already cached by BucketsCard.
 */
export function LifecycleOverviewCard({
  connectionId,
}: {
  connectionId: string
}) {
  const buckets = useWasabiBuckets(connectionId, false)
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState<LifecycleFilterStatus>('all')

  const { rows, summary } = useMemo(
    () => collectAccountLifecycle(buckets.data ?? []),
    [buckets.data]
  )
  const filtered = useMemo(
    () => filterAccountLifecycle(rows, query, status),
    [rows, query, status]
  )

  return (
    <Card>
      <CardHeader>
        <CardTitle>Lifecycle rules</CardTitle>
        <CardDescription>
          {buckets.isLoading
            ? 'Loading lifecycle rules across buckets…'
            : summary.readableBuckets === 0
              ? 'Read from each bucket via the S3 API on every sync.'
              : [
                  `${summary.bucketsWithRules} of ${summary.readableBuckets} bucket${summary.readableBuckets === 1 ? '' : 's'} have lifecycle rules`,
                  `${summary.enabledRules} enabled${summary.disabledRules > 0 ? `, ${summary.disabledRules} off` : ''}`,
                  summary.unreadableBuckets > 0
                    ? `${summary.unreadableBuckets} unreadable`
                    : '',
                ]
                  .filter(Boolean)
                  .join(' · ')}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {buckets.isLoading ? (
          <Skeleton className='h-24' />
        ) : buckets.error ? (
          <div className='text-sm text-danger-foreground'>
            {buckets.error.message}
          </div>
        ) : summary.totalBuckets === 0 ? (
          <p className='text-sm text-muted-foreground'>
            No buckets synced yet.
          </p>
        ) : summary.readableBuckets === 0 ? (
          <p className='text-sm text-muted-foreground'>
            Lifecycle rules could not be read for these buckets yet. Grant{' '}
            <code className='font-mono text-xs'>
              s3:GetLifecycleConfiguration
            </code>{' '}
            and run a sync.
          </p>
        ) : summary.totalRules === 0 ? (
          <p className='text-sm text-muted-foreground'>
            None of the {summary.readableBuckets} readable bucket
            {summary.readableBuckets === 1 ? '' : 's'} have lifecycle rules:
            objects and old versions stay until deleted.
          </p>
        ) : (
          <>
            {summary.earlyDeletionRules > 0 && (
              <div className='flex items-start gap-2 text-sm text-warning-foreground'>
                <TriangleAlert className='mt-0.5 h-4 w-4 shrink-0' />
                <span>
                  {summary.earlyDeletionRules} enabled rule
                  {summary.earlyDeletionRules === 1
                    ? ' deletes'
                    : 's delete'}{' '}
                  objects or old versions in under {WASABI_MIN_STORAGE_DAYS}{' '}
                  days; Wasabi Pay-Go still bills deleted data for the full{' '}
                  {WASABI_MIN_STORAGE_DAYS}-day minimum storage duration.
                </span>
              </div>
            )}

            <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
              <div className='relative w-full sm:max-w-xs'>
                <Search className='pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground' />
                <Input
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder='Filter bucket, rule, prefix, or tag…'
                  className='h-8 pl-8 text-sm'
                  aria-label='Filter lifecycle rules'
                />
              </div>
              <Tabs
                value={status}
                onValueChange={(v) => setStatus(v as LifecycleFilterStatus)}
              >
                <TabsList className='h-8'>
                  <TabsTrigger value='all' className='text-xs'>
                    All ({summary.totalRules})
                  </TabsTrigger>
                  <TabsTrigger value='enabled' className='text-xs'>
                    Enabled ({summary.enabledRules})
                  </TabsTrigger>
                  <TabsTrigger value='disabled' className='text-xs'>
                    Off ({summary.disabledRules})
                  </TabsTrigger>
                  {summary.earlyDeletionRules > 0 && (
                    <TabsTrigger value='early_deletion' className='text-xs'>
                      &lt; {WASABI_MIN_STORAGE_DAYS}d (
                      {summary.earlyDeletionRules})
                    </TabsTrigger>
                  )}
                </TabsList>
              </Tabs>
            </div>

            {filtered.length === 0 ? (
              <p className='text-sm text-muted-foreground'>
                No lifecycle rules match the current filter.
              </p>
            ) : (
              <div className='overflow-x-auto rounded-md border'>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Bucket</TableHead>
                      <TableHead>Rule</TableHead>
                      <TableHead>Applies to</TableHead>
                      <TableHead>Current versions</TableHead>
                      <TableHead>Noncurrent versions</TableHead>
                      <TableHead>Incomplete uploads</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {filtered.map((row) => (
                      <TableRow
                        key={`${row.bucket}:${row.rule.id || row.ruleIndex}`}
                        className={cn(
                          !row.rule.enabled && 'text-muted-foreground'
                        )}
                      >
                        <TableCell>
                          <Link
                            to='/connections/$connectionId/buckets/$bucket'
                            params={{ connectionId, bucket: row.bucket }}
                            className='font-medium hover:underline'
                          >
                            {row.bucket}
                          </Link>
                        </TableCell>
                        <TableCell>
                          <LifecycleRuleNameCell
                            rule={row.rule}
                            index={row.ruleIndex}
                          />
                        </TableCell>
                        <TableCell>
                          <LifecycleRuleScopeCell rule={row.rule} />
                        </TableCell>
                        <TableCell>
                          {formatCurrentVersionAction(row.rule)}
                        </TableCell>
                        <TableCell>
                          {formatNoncurrentVersionAction(row.rule)}
                        </TableCell>
                        <TableCell>
                          {formatAbortMultipartAction(row.rule)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
