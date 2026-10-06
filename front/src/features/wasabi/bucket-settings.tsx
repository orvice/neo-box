import type { ReactNode } from 'react'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import {
  ChevronDown,
  CircleAlert,
  Globe,
  Info,
  Lock,
  ShieldCheck,
  TriangleAlert,
} from 'lucide-react'
import {
  WasabiFindingSeverity,
  type WasabiBucketConfig,
  type WasabiBucketFinding,
} from '@/api/wasabi'
import { formatTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

export const POLICY_GUIDE = 'https://github.com/orvice/neo-box#bucket-settings'

const severityStyle: Record<
  WasabiFindingSeverity,
  { label: string; icon: typeof Info; className: string }
> = {
  [WasabiFindingSeverity.UNSPECIFIED]: {
    label: 'Note',
    icon: Info,
    className: 'text-muted-foreground',
  },
  [WasabiFindingSeverity.INFO]: {
    label: 'Info',
    icon: Info,
    className: 'text-running-foreground',
  },
  [WasabiFindingSeverity.WARNING]: {
    label: 'Warning',
    icon: TriangleAlert,
    className: 'text-warning-foreground',
  },
  [WasabiFindingSeverity.CRITICAL]: {
    label: 'Critical',
    icon: CircleAlert,
    className: 'text-danger-foreground',
  },
}

/** Compact badges for the bucket list: versioning, retention, public. */
export function SettingsBadges({ config }: { config?: WasabiBucketConfig }) {
  if (!config) return <span className='text-xs text-muted-foreground'>-</span>
  const warnings = config.findings.filter(
    (f) => f.severity === WasabiFindingSeverity.WARNING
  ).length
  return (
    <div className='flex flex-wrap items-center gap-1'>
      {config.public && (
        <Badge className='gap-1 bg-danger-muted text-danger-foreground'>
          <Globe className='h-3 w-3' />
          Public
        </Badge>
      )}
      {config.versioning?.status === 'Enabled' && (
        <Badge variant='secondary'>Versioned</Badge>
      )}
      {config.versioning?.status === 'Suspended' && (
        <Badge variant='secondary'>Versioning suspended</Badge>
      )}
      {config.objectLock && (
        <Badge variant='secondary' className='gap-1'>
          <Lock className='h-3 w-3' />
          Object Lock
        </Badge>
      )}
      {config.compliance && (
        <Badge variant='secondary' className='gap-1'>
          <ShieldCheck className='h-3 w-3' />
          Compliance
        </Badge>
      )}
      {warnings > 0 && (
        <span
          className='inline-flex items-center gap-1 text-xs text-warning-foreground'
          title='Settings worth a look; see the bucket page'
        >
          <TriangleAlert className='h-3 w-3' />
          {warnings}
        </span>
      )}
    </div>
  )
}

/** Everything read about a bucket's settings, with its flags first. */
export function BucketSettingsCard({ config }: { config: WasabiBucketConfig }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Settings</CardTitle>
        <CardDescription>
          Read through the S3 API {formatTime(config.fetchedAt)}, on every sync.
          Public status covers the bucket policy and ACL; Wasabi&apos;s console
          &ldquo;Public Access Override&rdquo; can&apos;t be read through the
          API.
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-6'>
        {config.findings.length > 0 && (
          <ul className='space-y-2'>
            {config.findings.map((f) => (
              <FindingRow key={f.code} finding={f} />
            ))}
          </ul>
        )}
        <div className='grid gap-6 lg:grid-cols-2'>
          <Section title='Protection'>
            <Row label='Versioning' error={config.errors.versioning}>
              {config.versioning?.status === 'Enabled'
                ? `On${config.versioning.mfaDelete ? ' · MFA delete' : ''}`
                : config.versioning?.status === 'Suspended'
                  ? 'Suspended'
                  : 'Off'}
            </Row>
            <Row label='Object Lock' error={config.errors.object_lock}>
              {config.objectLock
                ? [
                    'On',
                    config.objectLock.mode &&
                      `${titleCase(config.objectLock.mode)} mode`,
                    retention(config.objectLock.days, config.objectLock.years),
                  ]
                    .filter(Boolean)
                    .join(' · ')
                : 'Off'}
            </Row>
            <Row label='Compliance' error={config.errors.compliance}>
              {config.compliance
                ? [
                    `${config.compliance.retentionDays} days retention`,
                    config.compliance.conditionalHold && 'conditional hold',
                    config.compliance.deleteAfterRetention &&
                      'deleted after retention',
                    config.compliance.locked
                      ? `locked${config.compliance.lockTime ? ` since ${formatTime(config.compliance.lockTime)}` : ''}`
                      : config.compliance.lockTime &&
                        `locks ${formatTime(config.compliance.lockTime)}`,
                  ]
                    .filter(Boolean)
                    .join(' · ')
                : 'Off'}
            </Row>
          </Section>
          <Section title='Access'>
            <Row label='Public'>
              {config.public ? (
                <span className='inline-flex items-center gap-1 text-danger-foreground'>
                  <Globe className='h-3 w-3' /> Yes
                </span>
              ) : (
                'No (policy and ACL)'
              )}
            </Row>
            <Row label='Bucket policy' error={config.errors.policy}>
              {config.policy ? (
                <PolicyDocument document={config.policy.document} />
              ) : (
                'None'
              )}
            </Row>
            <Row label='ACL' error={config.errors.acl}>
              {config.acl?.grants.length ? (
                <ul className='space-y-0.5 text-end'>
                  {config.acl.grants.map((g, i) => (
                    <li key={i} className='break-all'>
                      <span className='font-mono text-xs'>
                        {g.grantee.replace(
                          'http://acs.amazonaws.com/groups/global/',
                          ''
                        )}
                      </span>{' '}
                      · {g.permission}
                    </li>
                  ))}
                </ul>
              ) : (
                'Owner only'
              )}
            </Row>
          </Section>
          <Section title='Other'>
            <Row label='Region' error={config.errors.location}>
              {config.region || '-'}
            </Row>
            <Row label='Created'>
              {config.createdAt
                ? timestampDate(config.createdAt).toLocaleDateString()
                : '-'}
            </Row>
            <Row label='Access logging' error={config.errors.logging}>
              {config.logging
                ? `to ${config.logging.targetBucket}/${config.logging.targetPrefix}`
                : 'Off'}
            </Row>
            <Row label='Replication' error={config.errors.replication}>
              {config.replicationRules.length
                ? config.replicationRules
                    .map(
                      (r) =>
                        `${r.prefix || 'all objects'} → ${r.destinationBucket}${r.enabled ? '' : ' (off)'}`
                    )
                    .join(', ')
                : 'Off'}
            </Row>
            <Row label='Tags' error={config.errors.tags}>
              {Object.keys(config.tags).length ? (
                <div className='flex flex-wrap justify-end gap-1'>
                  {Object.entries(config.tags).map(([k, v]) => (
                    <Badge key={k} variant='secondary' className='font-mono'>
                      {k}={v}
                    </Badge>
                  ))}
                </div>
              ) : (
                'None'
              )}
            </Row>
          </Section>
        </div>
        <Lifecycle config={config} />
      </CardContent>
    </Card>
  )
}

function Lifecycle({ config }: { config: WasabiBucketConfig }) {
  const days = (n: number) => (n > 0 ? `${n} days` : '-')
  return (
    <div className='space-y-2'>
      <div className='text-sm font-medium'>Lifecycle rules</div>
      {config.errors.lifecycle ? (
        <Unreadable reason={config.errors.lifecycle} />
      ) : config.lifecycleRules.length === 0 ? (
        <p className='text-sm text-muted-foreground'>
          No rules: objects and old versions stay until deleted.
        </p>
      ) : (
        <div className='overflow-x-auto rounded-md border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Rule</TableHead>
                <TableHead>Applies to</TableHead>
                <TableHead>Delete objects after</TableHead>
                <TableHead>Delete old versions after</TableHead>
                <TableHead>Abort uploads after</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {config.lifecycleRules.map((r, i) => (
                <TableRow
                  key={r.id || i}
                  className={cn(!r.enabled && 'text-muted-foreground')}
                >
                  <TableCell>
                    {r.id || `Rule ${i + 1}`}
                    {!r.enabled && (
                      <Badge variant='secondary' className='ms-2'>
                        Off
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className='font-mono text-xs'>
                    {r.prefix || 'all objects'}
                  </TableCell>
                  <TableCell>{days(r.expirationDays)}</TableCell>
                  <TableCell>{days(r.noncurrentDays)}</TableCell>
                  <TableCell>{days(r.abortMultipartDays)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}

function FindingRow({ finding: f }: { finding: WasabiBucketFinding }) {
  const style =
    severityStyle[f.severity] ??
    severityStyle[WasabiFindingSeverity.UNSPECIFIED]
  const Icon = style.icon
  return (
    <li className='flex items-start gap-2 text-sm'>
      <Icon className={cn('mt-0.5 h-4 w-4 shrink-0', style.className)} />
      <span>
        <span className={cn('font-medium', style.className)}>
          {style.label}:
        </span>{' '}
        {f.message}
        {f.code === 'settings_unreadable' && (
          <>
            {' '}
            <a
              href={POLICY_GUIDE}
              target='_blank'
              rel='noreferrer'
              className='underline underline-offset-2'
            >
              Policy to grant
            </a>
          </>
        )}
      </span>
    </li>
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
  error,
  children,
}: {
  label: string
  error?: string
  children: ReactNode
}) {
  return (
    <div className='flex items-start justify-between gap-4 py-1.5'>
      <dt className='shrink-0'>{label}</dt>
      <dd className='min-w-0 text-end'>
        {error ? <Unreadable reason={error} /> : children}
      </dd>
    </div>
  )
}

function Unreadable({ reason }: { reason: string }) {
  const text =
    reason === 'access_denied'
      ? 'Needs permission'
      : reason === 'not_supported'
        ? 'Not available on Wasabi'
        : reason
  return (
    <span
      className='text-xs text-muted-foreground'
      title={reason === 'access_denied' ? 'Grant it in the key policy' : reason}
    >
      {text}
    </span>
  )
}

function PolicyDocument({ document }: { document: string }) {
  let pretty = document
  try {
    pretty = JSON.stringify(JSON.parse(document), null, 2)
  } catch {
    // Show it as stored.
  }
  return (
    <Collapsible>
      <CollapsibleTrigger asChild>
        <Button variant='ghost' size='sm' className='h-6 px-2 text-xs'>
          Show JSON
          <ChevronDown className='h-3 w-3' />
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <pre className='mt-2 max-h-80 overflow-auto rounded-md bg-muted p-3 text-start font-mono text-xs'>
          {pretty}
        </pre>
      </CollapsibleContent>
    </Collapsible>
  )
}

function retention(days: number, years: number) {
  if (years > 0)
    return `${years} year${years === 1 ? '' : 's'} default retention`
  if (days > 0) return `${days} day${days === 1 ? '' : 's'} default retention`
  return ''
}

function titleCase(s: string) {
  return s.charAt(0) + s.slice(1).toLowerCase()
}
