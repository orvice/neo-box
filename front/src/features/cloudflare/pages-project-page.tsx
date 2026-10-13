import { useState } from 'react'
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router'
import {
  ChevronLeft,
  ExternalLink,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  Trash2,
  TriangleAlert,
} from 'lucide-react'
import { toast } from 'sonner'
import {
  CloudflarePagesEnvironment as Env,
  CloudflarePagesEnvVarType as EnvVarType,
  CloudflareResourceKind,
  useCloudflarePagesChanges,
  useCloudflarePagesDeployments,
  useCloudflarePagesProject,
  type CloudflarePagesDeployment,
  type CloudflarePagesDomain,
  type CloudflarePagesEnvVar,
  type GetCloudflarePagesProjectResponse,
} from '@/api/cloudflare'
import { useConnection } from '@/api/connections'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { DataTable, type Column } from '@/components/data-table'
import { toastChange } from './changed'
import { Pager, ProblemAlert, RefreshButton } from './list-parts'
import {
  bindingNameProblem,
  branchProblem,
  deploymentStatus,
  deploymentSucceeded,
  domainProblem,
  environmentLabel,
  pagesDevUrl,
  pagesRecordOutcome,
  secretValueProblem,
} from './manage'
import { ConfirmChangeDialog, FormDialog } from './manage-dialogs'
import { ResourceOperationsLog } from './resource-operations'
import { Section } from './section'
import { formatTime } from './tunnels'

const route = getRouteApi(
  '/_authenticated/connections/$connectionId/pages/$name'
)

type Dialog =
  | { kind: 'settings' }
  | { kind: 'domain' }
  | { kind: 'delete-domain'; domain: CloudflarePagesDomain }
  | { kind: 'var'; env: Env; existing?: CloudflarePagesEnvVar }
  | { kind: 'delete-var'; env: Env; variable: CloudflarePagesEnvVar }
  | { kind: 'retry'; deployment: CloudflarePagesDeployment }
  | { kind: 'rollback'; deployment: CloudflarePagesDeployment }
  | { kind: 'delete-deployment'; deployment: CloudflarePagesDeployment }
  | { kind: 'delete' }

/** One Pages project: settings, variables, deployments and domains. */
export function CloudflarePagesProjectPage() {
  const { connectionId, name } = route.useParams()
  const connection = useConnection(connectionId)
  const detail = useCloudflarePagesProject(connectionId, name)
  const project = detail.data
  const changes = useCloudflarePagesChanges(connectionId, name)
  const navigate = useNavigate()
  const [dialog, setDialog] = useState<Dialog>()
  const close = () => setDialog(undefined)
  const url = project && pagesDevUrl(project.subdomain)

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
          project?.name ??
          (detail.isLoading ? <Skeleton className='h-8 w-48' /> : name)
        }
        subtitle={
          project && (
            <span className='inline-flex flex-wrap items-center gap-2 text-xs'>
              {url && (
                <a
                  href={url}
                  target='_blank'
                  rel='noreferrer noopener'
                  className='inline-flex items-center gap-1 font-mono hover:underline'
                >
                  {project.subdomain} <ExternalLink className='h-3 w-3' />
                </a>
              )}
              <span>Created {formatTime(project.createdAt)}</span>
            </span>
          )
        }
        actions={
          <>
            <RefreshButton connectionId={connectionId} />
            <Button
              variant='outline'
              onClick={() => setDialog({ kind: 'delete' })}
              disabled={!project}
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
            permission='Cloudflare Pages · Read'
          />
        ) : (
          <Tabs defaultValue='overview'>
            <TabsList>
              <TabsTrigger value='overview'>Overview</TabsTrigger>
              <TabsTrigger value='deployments'>Deployments</TabsTrigger>
              <TabsTrigger value='variables'>Variables</TabsTrigger>
              <TabsTrigger value='operations'>Operation log</TabsTrigger>
            </TabsList>
            <TabsContent value='overview' className='space-y-8 pt-4'>
              {project?.issues.length ? (
                <Alert>
                  <TriangleAlert />
                  <AlertTitle>Some of this could not be read</AlertTitle>
                  <AlertDescription>
                    <ul className='list-disc ps-4'>
                      {project.issues.map((i) => (
                        <li key={i.source}>
                          {i.source}: {i.message}
                        </li>
                      ))}
                    </ul>
                  </AlertDescription>
                </Alert>
              ) : null}
              <SettingsSection
                project={project}
                loading={detail.isLoading}
                onEdit={() => setDialog({ kind: 'settings' })}
              />
              <DomainsSection
                domains={project?.domains}
                loading={detail.isLoading}
                onAdd={() => setDialog({ kind: 'domain' })}
                onDelete={(domain) =>
                  setDialog({ kind: 'delete-domain', domain })
                }
              />
            </TabsContent>
            <TabsContent value='deployments' className='pt-4'>
              <DeploymentsSection
                connectionId={connectionId}
                name={name}
                onAction={(kind, deployment) => setDialog({ kind, deployment })}
              />
            </TabsContent>
            <TabsContent value='variables' className='space-y-8 pt-4'>
              {([Env.PRODUCTION, Env.PREVIEW] as const).map((env) => (
                <VariablesSection
                  key={env}
                  env={env}
                  variables={
                    env === Env.PRODUCTION
                      ? project?.productionEnvVars
                      : project?.previewEnvVars
                  }
                  loading={detail.isLoading}
                  onAdd={() => setDialog({ kind: 'var', env })}
                  onEdit={(existing) =>
                    setDialog({ kind: 'var', env, existing })
                  }
                  onDelete={(variable) =>
                    setDialog({ kind: 'delete-var', env, variable })
                  }
                />
              ))}
            </TabsContent>
            <TabsContent value='operations' className='pt-4'>
              <ResourceOperationsLog
                connectionId={connectionId}
                kind={CloudflareResourceKind.PAGES}
                resource={name}
              />
            </TabsContent>
          </Tabs>
        )}
      </PageScroll>

      {dialog?.kind === 'settings' && project && (
        <SettingsDialog
          project={project}
          run={async (patch) => {
            const res = await changes.update.mutateAsync(patch)
            toastChange('Settings saved', res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'domain' && (
        <FormDialog
          title='Add a custom domain'
          description='The project answers on this hostname once Cloudflare has verified it.'
          submitLabel='Add domain'
          what='custom domains'
          fields={[
            {
              name: 'hostname',
              label: 'Hostname',
              placeholder: 'www.example.com',
              mono: true,
              check: domainProblem,
            },
          ]}
          toggles={[
            {
              name: 'createDns',
              label: 'Create the DNS record',
              hint: `A proxied CNAME to ${project?.subdomain ?? 'the project'}, when this account has the domain's zone. An existing record is never replaced.`,
              initial: true,
            },
          ]}
          run={async ({ text, on }) => {
            const res = await changes.addDomain.mutateAsync({
              hostname: text.hostname.toLowerCase(),
              createDns: on.createDns,
            })
            toastChange(`${text.hostname} added`, res.result)
            const o = pagesRecordOutcome(res.record)
            if (o.tone === 'success') toast.success(o.message)
            if (o.tone === 'warning')
              toast.warning(o.message, { duration: 10000 })
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'delete-domain' && (
        <ConfirmChangeDialog
          title={`Remove ${dialog.domain.name}?`}
          description='The project stops answering on this hostname. Its DNS record is not deleted.'
          confirmLabel='Remove domain'
          pendingLabel='Removing…'
          what='custom domains'
          run={async () => {
            const res = await changes.deleteDomain.mutateAsync(
              dialog.domain.name
            )
            toastChange(`${dialog.domain.name} removed`, res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'var' && (
        <FormDialog
          title={
            dialog.existing
              ? `Edit ${dialog.existing.name}`
              : `Add a ${environmentLabel(dialog.env).toLowerCase()} variable`
          }
          description='It applies to new deployments only; existing ones keep the values they were built with.'
          submitLabel='Save variable'
          what='variables'
          fields={[
            {
              name: 'name',
              label: 'Name',
              initial: dialog.existing?.name,
              disabled: !!dialog.existing,
              placeholder: 'API_URL',
              mono: true,
              check: bindingNameProblem,
            },
            {
              name: 'value',
              label: 'Value',
              initial:
                dialog.existing?.type === EnvVarType.PLAIN_TEXT
                  ? dialog.existing.value
                  : '',
              secret: dialog.existing?.type === EnvVarType.SECRET,
              mono: true,
              check: secretValueProblem,
            },
          ]}
          toggles={[
            {
              name: 'secret',
              label: 'Encrypt as a secret',
              hint: 'A secret can be replaced but its value is never shown again.',
              initial: dialog.existing?.type === EnvVarType.SECRET,
            },
          ]}
          run={async ({ text, on }) => {
            const res = await changes.setEnvVar.mutateAsync({
              environment: dialog.env,
              varName: text.name,
              value: text.value,
              secret: on.secret,
            })
            toastChange(`${text.name} saved`, res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'delete-var' && (
        <ConfirmChangeDialog
          title={`Delete ${dialog.variable.name}?`}
          description={`It is removed from the ${environmentLabel(dialog.env).toLowerCase()} environment for new deployments.`}
          confirmLabel='Delete variable'
          pendingLabel='Deleting…'
          what='variables'
          run={async () => {
            const res = await changes.deleteEnvVar.mutateAsync({
              environment: dialog.env,
              varName: dialog.variable.name,
            })
            toastChange(`${dialog.variable.name} deleted`, res.result)
          }}
          onClose={close}
        />
      )}
      {dialog?.kind === 'retry' && (
        <ConfirmChangeDialog
          title='Retry this deployment?'
          description='It is built again as a new deployment.'
          confirmLabel='Retry'
          pendingLabel='Retrying…'
          destructive={false}
          what='deployments'
          run={async () => {
            const res = await changes.retry.mutateAsync(dialog.deployment.id)
            toastChange('Deployment retried', res.result)
          }}
          onClose={close}
        >
          <DeploymentSummary d={dialog.deployment} />
        </ConfirmChangeDialog>
      )}
      {dialog?.kind === 'rollback' && (
        <ConfirmChangeDialog
          title='Roll back to this deployment?'
          description='It becomes the live production deployment right away.'
          confirmLabel='Roll back'
          pendingLabel='Rolling back…'
          destructive={false}
          what='deployments'
          run={async () => {
            const res = await changes.rollback.mutateAsync(dialog.deployment.id)
            toastChange('Rolled back', res.result)
          }}
          onClose={close}
        >
          <DeploymentSummary d={dialog.deployment} />
        </ConfirmChangeDialog>
      )}
      {dialog?.kind === 'delete-deployment' && (
        <ConfirmChangeDialog
          title='Delete this deployment?'
          description='Its URL stops working. Cloudflare refuses to delete the live production deployment.'
          confirmLabel='Delete deployment'
          pendingLabel='Deleting…'
          what='deployments'
          run={async () => {
            const res = await changes.deleteDeployment.mutateAsync(
              dialog.deployment.id
            )
            toastChange('Deployment deleted', res.result)
          }}
          onClose={close}
        >
          <DeploymentSummary d={dialog.deployment} />
        </ConfirmChangeDialog>
      )}
      {dialog?.kind === 'delete' && (
        <ConfirmChangeDialog
          title={`Delete ${name}?`}
          description='The project, all its deployments and its pages.dev address are gone. This cannot be undone.'
          confirmLabel='Delete project'
          pendingLabel='Deleting…'
          typeName={name}
          what='Pages projects'
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

function DeploymentSummary({ d }: { d: CloudflarePagesDeployment }) {
  return (
    <dl className='rounded-md border p-3 text-sm'>
      <dd className='font-mono text-xs break-all'>
        {d.shortId} · {environmentLabel(d.environment)}
        {d.branch && ` · ${d.branch}`}
      </dd>
      {d.commitMessage && (
        <dd className='text-xs text-muted-foreground'>{d.commitMessage}</dd>
      )}
      <dd className='text-xs text-muted-foreground'>
        Created {formatTime(d.createdAt)}
      </dd>
    </dl>
  )
}

function SettingsDialog({
  project,
  run,
  onClose,
}: {
  project: GetCloudflarePagesProjectResponse
  run: (patch: {
    productionBranch?: string
    buildCommand?: string
    destinationDir?: string
    rootDir?: string
  }) => Promise<void>
  onClose: () => void
}) {
  const bc = project.buildConfig
  const git = !!project.gitRepository
  const current = {
    productionBranch: project.productionBranch,
    buildCommand: bc?.buildCommand ?? '',
    destinationDir: bc?.destinationDir ?? '',
    rootDir: bc?.rootDir ?? '',
  }
  return (
    <FormDialog
      title='Edit settings'
      description='Build settings apply to the next deployment.'
      submitLabel='Save settings'
      what='project settings'
      fields={[
        {
          name: 'productionBranch',
          label: 'Production branch',
          initial: current.productionBranch,
          disabled: git,
          hint: git
            ? 'Managed by the connected Git repository; change it in the Cloudflare dashboard.'
            : undefined,
          mono: true,
          check: git ? undefined : branchProblem,
        },
        {
          name: 'buildCommand',
          label: 'Build command',
          initial: current.buildCommand,
          placeholder: 'npm run build',
          mono: true,
        },
        {
          name: 'destinationDir',
          label: 'Build output directory',
          initial: current.destinationDir,
          placeholder: 'dist',
          mono: true,
        },
        {
          name: 'rootDir',
          label: 'Root directory',
          initial: current.rootDir,
          placeholder: '/',
          mono: true,
        },
      ]}
      run={async ({ text }) => {
        // Only what changed is sent; Neo Box keeps the rest as it is.
        const patch: Record<string, string> = {}
        for (const [k, v] of Object.entries(current)) {
          if (text[k] !== v) patch[k] = text[k]
        }
        if (Object.keys(patch).length === 0) {
          toast.info('Already as requested; nothing changed')
          return
        }
        await run(patch)
      }}
      onClose={onClose}
    />
  )
}

function SettingsSection({
  project,
  loading,
  onEdit,
}: {
  project: GetCloudflarePagesProjectResponse | undefined
  loading: boolean
  onEdit: () => void
}) {
  if (loading || !project)
    return (
      <Section title='Settings'>
        <Skeleton className='h-32 w-full' />
      </Section>
    )
  const bc = project.buildConfig
  const rows: [string, string][] = [
    ['Production branch', project.productionBranch],
    ['Build command', bc?.buildCommand ?? ''],
    ['Build output directory', bc?.destinationDir ?? ''],
    ['Root directory', bc?.rootDir ?? ''],
  ]
  return (
    <Section
      title='Settings'
      action={
        <Button size='sm' variant='outline' onClick={onEdit}>
          <Pencil className='h-3.5 w-3.5' /> Edit
        </Button>
      }
    >
      <div className='space-y-3 rounded-md border p-4'>
        <dl className='grid grid-cols-[11rem_1fr] gap-x-3 gap-y-2 text-sm'>
          {rows.map(([label, value]) => (
            <div key={label} className='contents'>
              <dt className='text-muted-foreground'>{label}</dt>
              <dd className='font-mono text-xs'>{value || '-'}</dd>
            </div>
          ))}
          <dt className='text-muted-foreground'>Source</dt>
          <dd className='text-xs'>
            {project.gitRepository
              ? `${project.gitProvider || 'Git'}: ${project.gitRepository}`
              : 'Direct upload'}
          </dd>
        </dl>
      </div>
    </Section>
  )
}

function DomainsSection({
  domains,
  loading,
  onAdd,
  onDelete,
}: {
  domains: CloudflarePagesDomain[] | undefined
  loading: boolean
  onAdd: () => void
  onDelete: (d: CloudflarePagesDomain) => void
}) {
  const columns: Column<CloudflarePagesDomain>[] = [
    {
      header: 'Hostname',
      cell: (d) => (
        <a
          href={`https://${d.name}`}
          target='_blank'
          rel='noreferrer noopener'
          className='font-mono text-xs hover:underline'
        >
          {d.name}
        </a>
      ),
    },
    {
      header: 'Status',
      cell: (d) => (
        <Badge
          className={cn(
            d.status === 'active'
              ? 'bg-success-muted text-success-foreground'
              : 'bg-warning-muted text-warning-foreground'
          )}
        >
          {d.status || 'unknown'}
        </Badge>
      ),
    },
    {
      header: 'Actions',
      cell: (d) => (
        <Button
          size='sm'
          variant='outline'
          onClick={() => onDelete(d)}
          aria-label={`Remove ${d.name}`}
        >
          <Trash2 className='h-3.5 w-3.5' />
        </Button>
      ),
    },
  ]
  return (
    <Section
      title='Custom domains'
      description='Hostnames the project answers on, besides its pages.dev address.'
      action={
        <Button size='sm' onClick={onAdd}>
          <Plus className='h-4 w-4' /> Add domain
        </Button>
      }
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

function VariablesSection({
  env,
  variables,
  loading,
  onAdd,
  onEdit,
  onDelete,
}: {
  env: Env
  variables: CloudflarePagesEnvVar[] | undefined
  loading: boolean
  onAdd: () => void
  onEdit: (v: CloudflarePagesEnvVar) => void
  onDelete: (v: CloudflarePagesEnvVar) => void
}) {
  const columns: Column<CloudflarePagesEnvVar>[] = [
    {
      header: 'Name',
      cell: (v) => <span className='font-mono text-xs'>{v.name}</span>,
    },
    {
      header: 'Type',
      cell: (v) => (
        <Badge variant='secondary'>
          {v.type === EnvVarType.SECRET ? 'Secret' : 'Plain text'}
        </Badge>
      ),
    },
    {
      header: 'Value',
      cell: (v) =>
        v.type === EnvVarType.SECRET ? (
          <span className='text-xs text-muted-foreground'>Hidden</span>
        ) : (
          <span className='font-mono text-xs break-all'>{v.value}</span>
        ),
    },
    {
      header: 'Actions',
      cell: (v) => (
        <div className='flex gap-1'>
          <Button
            size='sm'
            variant='outline'
            onClick={() => onEdit(v)}
            aria-label={`Edit ${v.name}`}
          >
            <Pencil className='h-3.5 w-3.5' />
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={() => onDelete(v)}
            aria-label={`Delete ${v.name}`}
          >
            <Trash2 className='h-3.5 w-3.5' />
          </Button>
        </div>
      ),
    },
  ]
  return (
    <Section
      title={`${environmentLabel(env)} variables`}
      description={
        env === Env.PRODUCTION
          ? 'Environment variables of production deployments.'
          : 'Environment variables of preview deployments.'
      }
      action={
        <Button size='sm' onClick={onAdd}>
          <Plus className='h-4 w-4' /> Add variable
        </Button>
      }
    >
      <DataTable
        columns={columns}
        data={variables}
        isLoading={loading}
        emptyMessage='No variables'
      />
    </Section>
  )
}

function DeploymentsSection({
  connectionId,
  name,
  onAction,
}: {
  connectionId: string
  name: string
  onAction: (
    kind: 'retry' | 'rollback' | 'delete-deployment',
    d: CloudflarePagesDeployment
  ) => void
}) {
  const [env, setEnv] = useState<Env>(Env.PRODUCTION)
  const [page, setPage] = useState(1)
  const deployments = useCloudflarePagesDeployments(
    connectionId,
    name,
    env,
    page
  )

  const columns: Column<CloudflarePagesDeployment>[] = [
    {
      header: 'Deployment',
      cell: (d) => (
        <div className='min-w-0'>
          {d.url ? (
            <a
              href={d.url}
              target='_blank'
              rel='noreferrer noopener'
              className='inline-flex items-center gap-1 font-mono text-xs hover:underline'
            >
              {d.shortId} <ExternalLink className='h-3 w-3' />
            </a>
          ) : (
            <span className='font-mono text-xs'>{d.shortId}</span>
          )}
          {d.aliases.length > 0 && (
            <div className='text-xs break-all text-muted-foreground'>
              {d.aliases[0]}
            </div>
          )}
        </div>
      ),
    },
    {
      header: 'Source',
      cell: (d) => (
        <div className='max-w-xs text-xs'>
          <div>
            {d.branch || '-'}
            {d.commitHash && (
              <span className='font-mono text-muted-foreground'>
                {' '}
                · {d.commitHash.slice(0, 7)}
              </span>
            )}
          </div>
          {d.commitMessage && (
            <div className='truncate text-muted-foreground'>
              {d.commitMessage}
            </div>
          )}
        </div>
      ),
    },
    {
      header: 'Status',
      cell: (d) => {
        const s = deploymentStatus(d)
        return <Badge className={s.className}>{s.label}</Badge>
      },
    },
    {
      header: 'Created',
      cell: (d) => (
        <span className='text-xs text-muted-foreground'>
          {formatTime(d.createdAt)}
        </span>
      ),
    },
    {
      header: 'Actions',
      cell: (d) => (
        <div className='flex gap-1'>
          <Button
            size='sm'
            variant='outline'
            onClick={() => onAction('retry', d)}
            title='Build again'
            aria-label={`Retry ${d.shortId}`}
          >
            <RefreshCw className='h-3.5 w-3.5' />
          </Button>
          {d.environment === Env.PRODUCTION && deploymentSucceeded(d) && (
            <Button
              size='sm'
              variant='outline'
              onClick={() => onAction('rollback', d)}
              title='Make this the live deployment'
              aria-label={`Roll back to ${d.shortId}`}
            >
              <RotateCcw className='h-3.5 w-3.5' />
            </Button>
          )}
          <Button
            size='sm'
            variant='outline'
            onClick={() => onAction('delete-deployment', d)}
            aria-label={`Delete ${d.shortId}`}
          >
            <Trash2 className='h-3.5 w-3.5' />
          </Button>
        </div>
      ),
    },
  ]

  return (
    <Section
      title='Deployments'
      description='Newest first.'
      action={
        <div className='flex gap-1'>
          {([Env.PRODUCTION, Env.PREVIEW] as const).map((e) => (
            <Button
              key={e}
              size='sm'
              variant={env === e ? 'default' : 'outline'}
              onClick={() => {
                setEnv(e)
                setPage(1)
              }}
            >
              {environmentLabel(e)}
            </Button>
          ))}
        </div>
      }
    >
      {deployments.error ? (
        <ProblemAlert
          error={deployments.error}
          permission='Cloudflare Pages · Read'
        />
      ) : (
        <>
          <DataTable
            columns={columns}
            data={deployments.data?.deployments}
            isLoading={deployments.isLoading}
            emptyMessage={`No ${environmentLabel(env).toLowerCase()} deployments`}
          />
          <Pager
            info={deployments.data?.pageInfo}
            onPage={setPage}
            busy={deployments.isFetching}
          />
        </>
      )}
    </Section>
  )
}
