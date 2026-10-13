import { useState } from 'react'
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router'
import {
  ChevronLeft,
  ExternalLink,
  Plus,
  RotateCcw,
  Trash2,
  TriangleAlert,
} from 'lucide-react'
import {
  CloudflareResourceKind,
  useCloudflareWorker,
  useCloudflareWorkerChanges,
  type CloudflareWorkerDeployment,
  type CloudflareWorkerDomain,
  type CloudflareWorkerRoute,
  type CloudflareWorkerSecret,
  type CloudflareWorkerVersion,
} from '@/api/cloudflare'
import { useConnection } from '@/api/connections'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { DataTable, type Column } from '@/components/data-table'
import { toastChange, toastFailure } from './changed'
import { ProblemAlert, RefreshButton } from './list-parts'
import {
  bindingNameProblem,
  canRollbackTo,
  cronProblem,
  domainProblem,
  routeProblem,
  secretValueProblem,
  servedVersions,
  workersDevUrl,
} from './manage'
import { ConfirmChangeDialog, FormDialog } from './manage-dialogs'
import { ResourceOperationsLog } from './resource-operations'
import { Section } from './section'
import { formatTime } from './tunnels'

const route = getRouteApi(
  '/_authenticated/connections/$connectionId/workers/$name'
)

type Dialog =
  | { kind: 'domain' }
  | { kind: 'detach'; domain: CloudflareWorkerDomain }
  | { kind: 'route' }
  | { kind: 'delete-route'; route: CloudflareWorkerRoute }
  | { kind: 'cron' }
  | { kind: 'delete-cron'; cron: string }
  | { kind: 'secret'; replace?: CloudflareWorkerSecret }
  | { kind: 'delete-secret'; secret: CloudflareWorkerSecret }
  | { kind: 'rollback'; version: CloudflareWorkerVersion }
  | { kind: 'delete' }

/** One Worker: how it is reached, its triggers, secrets and versions. */
export function CloudflareWorkerPage() {
  const { connectionId, name } = route.useParams()
  const connection = useConnection(connectionId)
  const detail = useCloudflareWorker(connectionId, name)
  const worker = detail.data
  const changes = useCloudflareWorkerChanges(connectionId, name)
  const navigate = useNavigate()
  const [dialog, setDialog] = useState<Dialog>()
  const close = () => setDialog(undefined)

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
          worker?.name ??
          (detail.isLoading ? <Skeleton className='h-8 w-48' /> : name)
        }
        subtitle={
          worker && (
            <span className='inline-flex flex-wrap items-center gap-2 text-xs'>
              <span>Created {formatTime(worker.createdAt)}</span>
              <span>Modified {formatTime(worker.modifiedAt)}</span>
            </span>
          )
        }
        actions={
          <>
            <RefreshButton connectionId={connectionId} />
            <Button
              variant='outline'
              onClick={() => setDialog({ kind: 'delete' })}
              disabled={!worker}
            >
              <Trash2 className='h-4 w-4' /> Delete
            </Button>
          </>
        }
      />
      <PageScroll>
        {detail.error ? (
          <ProblemAlert
            error={detail.error}
            permission='Workers Scripts · Read'
          />
        ) : (
          <Tabs defaultValue='manage'>
            <TabsList>
              <TabsTrigger value='manage'>Manage</TabsTrigger>
              <TabsTrigger value='operations'>Operation log</TabsTrigger>
            </TabsList>
            <TabsContent value='manage' className='space-y-8 pt-4'>
              {worker?.issues.length ? (
                <Alert>
                  <TriangleAlert />
                  <AlertTitle>Some of this could not be read</AlertTitle>
                  <AlertDescription>
                    <ul className='list-disc ps-4'>
                      {worker.issues.map((i) => (
                        <li key={i.source}>
                          {i.source}: {i.message}
                        </li>
                      ))}
                    </ul>
                  </AlertDescription>
                </Alert>
              ) : null}
              <OverviewSection
                worker={worker}
                loading={detail.isLoading}
                onToggle={(enabled) =>
                  changes.setWorkersDev.mutate(enabled, {
                    onSuccess: (res) =>
                      toastChange(
                        enabled
                          ? 'workers.dev enabled'
                          : 'workers.dev disabled',
                        res.result
                      ),
                    onError: (err) => toastFailure(err, 'workers.dev setting'),
                  })
                }
                toggling={changes.setWorkersDev.isPending}
              />
              <DomainsSection
                domains={worker?.domains}
                loading={detail.isLoading}
                onAdd={() => setDialog({ kind: 'domain' })}
                onDetach={(domain) => setDialog({ kind: 'detach', domain })}
              />
              <RoutesSection
                routes={worker?.routes}
                loading={detail.isLoading}
                onAdd={() => setDialog({ kind: 'route' })}
                onDelete={(r) => setDialog({ kind: 'delete-route', route: r })}
              />
              <CronsSection
                crons={worker?.crons}
                loading={detail.isLoading}
                onAdd={() => setDialog({ kind: 'cron' })}
                onDelete={(cron) => setDialog({ kind: 'delete-cron', cron })}
              />
              <SecretsSection
                secrets={worker?.secrets}
                loading={detail.isLoading}
                onAdd={() => setDialog({ kind: 'secret' })}
                onReplace={(replace) => setDialog({ kind: 'secret', replace })}
                onDelete={(secret) =>
                  setDialog({ kind: 'delete-secret', secret })
                }
              />
              <VersionsSection
                versions={worker?.versions}
                deployments={worker?.deployments}
                loading={detail.isLoading}
                onRollback={(version) =>
                  setDialog({ kind: 'rollback', version })
                }
              />
            </TabsContent>
            <TabsContent value='operations' className='pt-4'>
              <ResourceOperationsLog
                connectionId={connectionId}
                kind={CloudflareResourceKind.WORKER}
                resource={name}
              />
            </TabsContent>
          </Tabs>
        )}
      </PageScroll>

      {dialog?.kind === 'domain' && (
        <FormDialog
          title='Add a custom domain'
          description='The Worker answers on this hostname. Its zone must be in this account; Cloudflare creates the DNS record and certificate.'
          submitLabel='Add domain'
          what='custom domains'
          fields={[
            {
              name: 'hostname',
              label: 'Hostname',
              placeholder: 'api.example.com',
              mono: true,
              check: domainProblem,
            },
          ]}
          run={async ({ text }) => {
            const res = await changes.attachDomain.mutateAsync(
              text.hostname.toLowerCase()
            )
            toastChange(`${text.hostname} attached`, res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'detach' && (
        <ConfirmChangeDialog
          title={`Detach ${dialog.domain.hostname}?`}
          description='The Worker stops answering on this hostname. Its DNS record is left to Cloudflare.'
          confirmLabel='Detach domain'
          pendingLabel='Detaching…'
          what='custom domains'
          run={async () => {
            const res = await changes.detachDomain.mutateAsync(
              dialog.domain.hostname
            )
            toastChange(`${dialog.domain.hostname} detached`, res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'route' && (
        <FormDialog
          title='Add a route'
          description='Requests matching the pattern run this Worker. The pattern must belong to a zone in this account, and is refused when another Worker already uses it.'
          submitLabel='Add route'
          what='routes'
          fields={[
            {
              name: 'pattern',
              label: 'Route pattern',
              hint: 'e.g. example.com/* or *.example.com/api/*',
              placeholder: 'example.com/*',
              mono: true,
              check: routeProblem,
            },
          ]}
          run={async ({ text }) => {
            const res = await changes.addRoute.mutateAsync(text.pattern)
            toastChange(`Route ${text.pattern} added`, res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'delete-route' && (
        <ConfirmChangeDialog
          title='Delete this route?'
          description='Matching requests stop reaching the Worker.'
          confirmLabel='Delete route'
          pendingLabel='Deleting…'
          what='routes'
          run={async () => {
            const res = await changes.deleteRoute.mutateAsync({
              zoneId: dialog.route.zoneId,
              routeId: dialog.route.id,
            })
            toastChange('Route deleted', res.result)
          }}
          onClose={close}
        >
          <dl className='rounded-md border p-3 text-sm'>
            <dd className='font-mono text-xs break-all'>
              {dialog.route.pattern}
            </dd>
            <dd className='text-xs text-muted-foreground'>
              {dialog.route.zoneName}
            </dd>
          </dl>
        </ConfirmChangeDialog>
      )}
      {dialog?.kind === 'cron' && (
        <FormDialog
          title='Add a cron trigger'
          description='The Worker runs on this schedule (UTC).'
          submitLabel='Add trigger'
          what='cron triggers'
          fields={[
            {
              name: 'cron',
              label: 'Cron expression',
              hint: 'Five fields, e.g. */15 * * * * for every 15 minutes',
              placeholder: '*/15 * * * *',
              mono: true,
              check: cronProblem,
            },
          ]}
          run={async ({ text }) => {
            const cron = text.cron.split(/\s+/).join(' ')
            const res = await changes.addCron.mutateAsync(cron)
            toastChange(`Cron trigger ${cron} added`, res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'delete-cron' && (
        <ConfirmChangeDialog
          title='Delete this cron trigger?'
          description={<span className='font-mono text-xs'>{dialog.cron}</span>}
          confirmLabel='Delete trigger'
          pendingLabel='Deleting…'
          what='cron triggers'
          run={async () => {
            const res = await changes.deleteCron.mutateAsync(dialog.cron)
            toastChange('Cron trigger deleted', res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'secret' && (
        <FormDialog
          title={dialog.replace ? 'Replace secret' : 'Add a secret'}
          description='The value is write-only: Neo Box does not keep it, and Cloudflare never shows it again.'
          submitLabel={dialog.replace ? 'Replace secret' : 'Save secret'}
          what='secrets'
          fields={[
            {
              name: 'name',
              label: 'Name',
              initial: dialog.replace?.name,
              disabled: !!dialog.replace,
              placeholder: 'API_KEY',
              mono: true,
              check: bindingNameProblem,
            },
            {
              name: 'value',
              label: 'Value',
              secret: true,
              check: secretValueProblem,
            },
          ]}
          run={async ({ text }) => {
            const res = await changes.putSecret.mutateAsync({
              secretName: text.name,
              value: text.value,
            })
            toastChange(`Secret ${text.name} saved`, res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'delete-secret' && (
        <ConfirmChangeDialog
          title={`Delete secret ${dialog.secret.name}?`}
          description='The Worker can no longer read it. The value cannot be recovered afterwards.'
          confirmLabel='Delete secret'
          pendingLabel='Deleting…'
          what='secrets'
          run={async () => {
            const res = await changes.deleteSecret.mutateAsync(
              dialog.secret.name
            )
            toastChange(`Secret ${dialog.secret.name} deleted`, res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'rollback' && (
        <ConfirmChangeDialog
          title={`Roll back to version ${dialog.version.number}?`}
          description='All traffic goes to that version right away, as a new deployment.'
          confirmLabel='Roll back'
          pendingLabel='Rolling back…'
          destructive={false}
          what='deployments'
          run={async () => {
            const res = await changes.rollback.mutateAsync(dialog.version.id)
            toastChange(
              `Rolled back to version ${dialog.version.number}`,
              res.result
            )
          }}
          onClose={close}
        >
          <dl className='rounded-md border p-3 text-sm'>
            <dd className='font-mono text-xs break-all'>{dialog.version.id}</dd>
            <dd className='text-xs text-muted-foreground'>
              Created {formatTime(dialog.version.createdAt)}
            </dd>
          </dl>
        </ConfirmChangeDialog>
      )}
      {dialog?.kind === 'delete' && (
        <ConfirmChangeDialog
          title={`Delete ${name}?`}
          description='The Worker stops answering everywhere, and its code, versions, secrets and cron triggers are gone. This cannot be undone.'
          confirmLabel='Delete Worker'
          pendingLabel='Deleting…'
          typeName={name}
          what='Workers'
          run={async () => {
            const res = await changes.remove.mutateAsync()
            toastChange(`Deleted ${name}`, res.result)
            void navigate({
              to: '/connections/$connectionId',
              params: { connectionId },
            })
          }}
          onClose={close}
        />
      )}
    </Page>
  )
}

function OverviewSection({
  worker,
  loading,
  onToggle,
  toggling,
}: {
  worker: ReturnType<typeof useCloudflareWorker>['data']
  loading: boolean
  onToggle: (enabled: boolean) => void
  toggling: boolean
}) {
  if (loading || !worker)
    return (
      <Section title='Overview'>
        <Skeleton className='h-24 w-full' />
      </Section>
    )
  const url = workersDevUrl(worker.name, worker.workersDevSubdomain)
  const known = worker.workersDevEnabled !== undefined
  return (
    <Section title='Overview'>
      <div className='space-y-4 rounded-md border p-4'>
        <div className='flex items-start justify-between gap-4'>
          <div className='min-w-0'>
            <div className='font-medium'>workers.dev</div>
            <div className='text-sm text-muted-foreground'>
              {url ? (
                worker.workersDevEnabled ? (
                  <a
                    href={url}
                    target='_blank'
                    rel='noreferrer noopener'
                    className='inline-flex items-center gap-1 font-mono text-xs hover:underline'
                  >
                    {url} <ExternalLink className='h-3 w-3' />
                  </a>
                ) : (
                  <span className='font-mono text-xs line-through'>{url}</span>
                )
              ) : (
                'This account has no workers.dev subdomain yet; enabling it creates one.'
              )}
            </div>
          </div>
          <Switch
            checked={worker.workersDevEnabled ?? false}
            onCheckedChange={onToggle}
            disabled={!known || toggling}
            aria-label='workers.dev'
          />
        </div>
        <dl className='grid grid-cols-[9rem_1fr] gap-x-3 gap-y-2 text-sm'>
          <dt className='text-muted-foreground'>Compatibility date</dt>
          <dd className='font-mono text-xs'>
            {worker.compatibilityDate || '-'}
            {worker.compatibilityFlags.length > 0 &&
              ` · ${worker.compatibilityFlags.join(', ')}`}
          </dd>
          <dt className='text-muted-foreground'>Bindings</dt>
          <dd className='flex flex-wrap gap-1'>
            {worker.bindings.length === 0 && (
              <span className='text-xs text-muted-foreground'>None</span>
            )}
            {worker.bindings.map((b) => (
              <Badge key={b.name} variant='secondary' title={b.type}>
                {b.name}
                <span className='ms-1 text-muted-foreground'>{b.type}</span>
              </Badge>
            ))}
          </dd>
        </dl>
      </div>
    </Section>
  )
}

function AddButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button size='sm' onClick={onClick}>
      <Plus className='h-4 w-4' /> {label}
    </Button>
  )
}

function DeleteButton({
  label,
  onClick,
}: {
  label: string
  onClick: () => void
}) {
  return (
    <Button
      size='sm'
      variant='outline'
      onClick={onClick}
      aria-label={label}
      title={label}
    >
      <Trash2 className='h-3.5 w-3.5' />
    </Button>
  )
}

function DomainsSection({
  domains,
  loading,
  onAdd,
  onDetach,
}: {
  domains: CloudflareWorkerDomain[] | undefined
  loading: boolean
  onAdd: () => void
  onDetach: (d: CloudflareWorkerDomain) => void
}) {
  const columns: Column<CloudflareWorkerDomain>[] = [
    {
      header: 'Hostname',
      cell: (d) => (
        <a
          href={`https://${d.hostname}`}
          target='_blank'
          rel='noreferrer noopener'
          className='font-mono text-xs hover:underline'
        >
          {d.hostname}
        </a>
      ),
    },
    {
      header: 'Zone',
      cell: (d) => <span className='text-xs'>{d.zoneName || '-'}</span>,
    },
    {
      header: 'Actions',
      cell: (d) => (
        <DeleteButton
          label={`Detach ${d.hostname}`}
          onClick={() => onDetach(d)}
        />
      ),
    },
  ]
  return (
    <Section
      title='Custom domains'
      description='Hostnames the Worker answers on, with DNS and certificate managed by Cloudflare.'
      action={<AddButton label='Add domain' onClick={onAdd} />}
    >
      <DataTable
        columns={columns}
        data={domains}
        isLoading={loading}
        emptyMessage='No custom domains'
      />
    </Section>
  )
}

function RoutesSection({
  routes,
  loading,
  onAdd,
  onDelete,
}: {
  routes: CloudflareWorkerRoute[] | undefined
  loading: boolean
  onAdd: () => void
  onDelete: (r: CloudflareWorkerRoute) => void
}) {
  const columns: Column<CloudflareWorkerRoute>[] = [
    {
      header: 'Pattern',
      cell: (r) => <span className='font-mono text-xs'>{r.pattern}</span>,
    },
    {
      header: 'Zone',
      cell: (r) => <span className='text-xs'>{r.zoneName || '-'}</span>,
    },
    {
      header: 'Actions',
      cell: (r) => (
        <DeleteButton
          label={`Delete ${r.pattern}`}
          onClick={() => onDelete(r)}
        />
      ),
    },
  ]
  return (
    <Section
      title='Routes'
      description='URL patterns that run the Worker.'
      action={<AddButton label='Add route' onClick={onAdd} />}
    >
      <DataTable
        columns={columns}
        data={routes}
        isLoading={loading}
        emptyMessage='No routes'
      />
    </Section>
  )
}

function CronsSection({
  crons,
  loading,
  onAdd,
  onDelete,
}: {
  crons: string[] | undefined
  loading: boolean
  onAdd: () => void
  onDelete: (cron: string) => void
}) {
  const columns: Column<string>[] = [
    {
      header: 'Schedule (UTC)',
      cell: (c) => <span className='font-mono text-xs'>{c}</span>,
    },
    {
      header: 'Actions',
      cell: (c) => (
        <DeleteButton label={`Delete ${c}`} onClick={() => onDelete(c)} />
      ),
    },
  ]
  return (
    <Section
      title='Cron triggers'
      description='Schedules on which the Worker runs.'
      action={<AddButton label='Add trigger' onClick={onAdd} />}
    >
      <DataTable
        columns={columns}
        data={crons}
        isLoading={loading}
        emptyMessage='No cron triggers'
      />
    </Section>
  )
}

function SecretsSection({
  secrets,
  loading,
  onAdd,
  onReplace,
  onDelete,
}: {
  secrets: CloudflareWorkerSecret[] | undefined
  loading: boolean
  onAdd: () => void
  onReplace: (s: CloudflareWorkerSecret) => void
  onDelete: (s: CloudflareWorkerSecret) => void
}) {
  const columns: Column<CloudflareWorkerSecret>[] = [
    {
      header: 'Name',
      cell: (s) => <span className='font-mono text-xs'>{s.name}</span>,
    },
    {
      header: 'Value',
      cell: () => <span className='text-xs text-muted-foreground'>Hidden</span>,
    },
    {
      header: 'Actions',
      cell: (s) => (
        <div className='flex gap-1'>
          <Button size='sm' variant='outline' onClick={() => onReplace(s)}>
            Replace
          </Button>
          <DeleteButton
            label={`Delete ${s.name}`}
            onClick={() => onDelete(s)}
          />
        </div>
      ),
    },
  ]
  return (
    <Section
      title='Secrets'
      description='Encrypted values the Worker reads at run time. Only their names are shown.'
      action={<AddButton label='Add secret' onClick={onAdd} />}
    >
      <DataTable
        columns={columns}
        data={secrets}
        isLoading={loading}
        emptyMessage='No secrets'
      />
    </Section>
  )
}

function VersionsSection({
  versions,
  deployments,
  loading,
  onRollback,
}: {
  versions: CloudflareWorkerVersion[] | undefined
  deployments: CloudflareWorkerDeployment[] | undefined
  loading: boolean
  onRollback: (v: CloudflareWorkerVersion) => void
}) {
  const served = servedVersions(deployments ?? [])
  const columns: Column<CloudflareWorkerVersion>[] = [
    {
      header: 'Version',
      cell: (v) => (
        <div>
          <div className='font-medium'>#{v.number}</div>
          <div className='font-mono text-xs text-muted-foreground' title={v.id}>
            {v.id.slice(0, 8)}
          </div>
        </div>
      ),
    },
    {
      header: 'Created',
      cell: (v) => (
        <div className='text-xs'>
          <div>{formatTime(v.createdAt)}</div>
          <div className='text-muted-foreground'>
            {[v.source, v.authorEmail].filter(Boolean).join(' · ')}
          </div>
        </div>
      ),
    },
    {
      header: 'Traffic',
      cell: (v) => {
        const pct = served.get(v.id)
        return pct === undefined ? (
          <span className='text-xs text-muted-foreground'>-</span>
        ) : (
          <Badge className='bg-success-muted text-success-foreground'>
            {pct}%
          </Badge>
        )
      },
    },
    {
      header: 'Actions',
      cell: (v) => (
        <Button
          size='sm'
          variant='outline'
          disabled={!canRollbackTo(v.id, served)}
          onClick={() => onRollback(v)}
        >
          <RotateCcw className='h-3.5 w-3.5' /> Roll back
        </Button>
      ),
    },
  ]
  return (
    <Section
      title='Versions'
      description='Recent versions of the Worker. Rolling back deploys an earlier version to all traffic.'
    >
      <DataTable
        columns={columns}
        data={versions}
        isLoading={loading}
        emptyMessage='No versions'
      />
    </Section>
  )
}
