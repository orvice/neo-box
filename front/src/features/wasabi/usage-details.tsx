import type { ReactNode } from 'react'
import { type WasabiUsage } from '@/api/wasabi'
import { formatBytes } from '@/lib/format'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { formatDayLong } from './format'

const count = new Intl.NumberFormat('en-US')

/**
 * Every figure the Stats API reported for one day of the account or a
 * bucket: storage as of midnight UTC, activity over the day.
 */
export function UsageDetailsCard({
  usage,
  account,
}: {
  usage: WasabiUsage
  /** Account rows also carry the 1 TB minimum top-up. */
  account?: boolean
}) {
  const n = (v: bigint) => count.format(v)
  return (
    <Card>
      <CardHeader>
        <CardTitle>Newest day · {formatDayLong(usage.day)}</CardTitle>
        <CardDescription>
          Everything Wasabi reported for this day. Storage is as of midnight
          UTC; activity covers the whole day.
        </CardDescription>
      </CardHeader>
      <CardContent className='grid gap-8 md:grid-cols-2'>
        <Section title='Storage'>
          <Row
            label='Active (billed)'
            hint='Padded bytes plus metadata'
            value={formatBytes(usage.activeStorageBytes)}
          />
          <Row
            label='Raw object bytes'
            value={formatBytes(usage.rawStorageBytes)}
          />
          <Row
            label='Padded object bytes'
            hint='With the minimum object size applied'
            value={formatBytes(usage.paddedStorageBytes)}
          />
          <Row
            label='Metadata'
            value={formatBytes(usage.metadataStorageBytes)}
          />
          <Row
            label='Deleted, still billed'
            hint='Under the minimum storage duration'
            value={formatBytes(usage.deletedStorageBytes)}
          />
          <Row
            label='Orphaned'
            value={formatBytes(usage.orphanedStorageBytes)}
          />
          {account && (
            <Row
              label='1 TB minimum top-up'
              hint='Billed because active storage is under 1 TB'
              value={formatBytes(usage.minStorageChargeBytes)}
            />
          )}
          <Row label='Objects' value={n(usage.billableObjects)} />
          <Row
            label='Deleted objects, still billed'
            value={n(usage.billableDeletedObjects)}
          />
        </Section>
        <Section title='Activity'>
          <Row label='Uploaded' value={formatBytes(usage.uploadBytes)} />
          <Row
            label='Downloaded (egress)'
            value={formatBytes(usage.downloadBytes)}
          />
          <Row
            label='Written to storage'
            value={formatBytes(usage.storageWroteBytes)}
          />
          <Row
            label='Read from storage'
            value={formatBytes(usage.storageReadBytes)}
          />
          <Row label='Deleted' value={formatBytes(usage.deleteBytes)} />
          <Row label='API calls' value={n(usage.apiCalls)} />
          <Row
            label='GET · PUT · DELETE'
            value={`${n(usage.getCalls)} · ${n(usage.putCalls)} · ${n(usage.deleteCalls)}`}
          />
          <Row
            label='LIST · HEAD'
            value={`${n(usage.listCalls)} · ${n(usage.headCalls)}`}
          />
        </Section>
      </CardContent>
    </Card>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className='space-y-2'>
      <div className='text-sm font-medium'>{title}</div>
      <dl className='divide-y text-sm'>{children}</dl>
    </div>
  )
}

function Row({
  label,
  hint,
  value,
}: {
  label: string
  hint?: string
  value: string
}) {
  return (
    <div className='flex items-baseline justify-between gap-4 py-1.5'>
      <dt>
        {label}
        {hint && (
          <span className='block text-xs text-muted-foreground'>{hint}</span>
        )}
      </dt>
      <dd className='font-medium tabular-nums'>{value}</dd>
    </div>
  )
}
