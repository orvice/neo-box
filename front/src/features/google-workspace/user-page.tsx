import { getRouteApi, Link, useNavigate } from '@tanstack/react-router'
import { ChevronLeft } from 'lucide-react'
import { useConnection } from '@/api/connections'
import { useWorkspaceTenants, useWorkspaceUser } from '@/api/google-workspace'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { useGmailConfirmation } from './gmail-access'
import { UserGmail } from './gmail-tab'
import { lastLoginText, triState } from './observability-format'
import { UserExposedFiles } from './user-files'
import { UserAliases, UserGroups, UserSecurity } from './user-tools'
import { StorageCard } from './user-usage-panel'

const route = getRouteApi(
  '/_authenticated/connections/$connectionId/tenants/$tenantId/users/$userId'
)

/**
 * One Workspace user, on a page of its own: the facts that matter at the top
 * (status, storage, last sign-in, 2-step), then tabs for the details.
 */
export function WorkspaceUserPage() {
  const { connectionId, tenantId, userId } = route.useParams()
  const { tab = 'profile' } = route.useSearch()
  const navigate = useNavigate()
  const connection = useConnection(connectionId)
  const tenants = useWorkspaceTenants(connectionId)
  const tenant = tenants.data?.find((t) => t.id === tenantId)
  const detail = useWorkspaceUser(connectionId, tenantId, userId)
  const gmail = useGmailConfirmation()
  const driveFiles = useGmailConfirmation()
  const u = detail.data

  return (
    <Page>
      <PageHeader
        breadcrumb={
          <Link
            to='/connections/$connectionId'
            params={{ connectionId }}
            search={{ tenant: tenantId }}
            className='inline-flex items-center gap-1'
          >
            <ChevronLeft className='h-3 w-3' />
            {connection.data?.name ?? 'Google Workspace'}
            {tenant ? ` · ${tenant.name}` : ''}
          </Link>
        }
        title={
          u?.primaryEmail ??
          (detail.isLoading ? <Skeleton className='h-8 w-64' /> : 'User')
        }
        subtitle={
          u && (
            <span className='inline-flex flex-wrap items-center gap-2'>
              {u.givenName} {u.familyName}
              <Badge variant={u.suspended ? 'destructive' : 'secondary'}>
                {u.suspended ? 'Suspended' : 'Active'}
              </Badge>
              {u.isAdmin && <Badge variant='outline'>Admin</Badge>}
            </span>
          )
        }
      />
      <PageScroll className='space-y-6'>
        {detail.error ? (
          <Alert variant='destructive'>
            <AlertDescription>{String(detail.error)}</AlertDescription>
          </Alert>
        ) : !u ? (
          <Skeleton className='h-96' />
        ) : (
          <>
            <div className='grid gap-4 md:grid-cols-2 xl:grid-cols-4'>
              <StorageCard
                connectionId={connectionId}
                tenantId={tenantId}
                userId={userId}
              />
              <Summary
                label='Last sign-in'
                value={lastLoginText(u.lastLoginTime)}
                note={
                  u.creationTime
                    ? `Created ${new Date(u.creationTime).toLocaleDateString()}`
                    : undefined
                }
              />
              <Summary
                label='2-step verification'
                value={
                  u.twoStepEnrolled === undefined
                    ? 'Unknown'
                    : u.twoStepEnrolled
                      ? 'Enrolled'
                      : 'Not enrolled'
                }
                note={`Enforced: ${triState(u.twoStepEnforced)}`}
              />
              <Summary
                label='Organization unit'
                value={u.orgUnitPath || '/'}
                note={
                  u.changePasswordAtNextLogin
                    ? 'Must change password at next sign-in'
                    : undefined
                }
              />
            </div>
            <Tabs
              value={tab}
              onValueChange={(next) =>
                void navigate({
                  to: '.',
                  search: { tab: next as typeof tab },
                  replace: true,
                })
              }
            >
              <TabsList>
                <TabsTrigger value='profile'>Profile</TabsTrigger>
                <TabsTrigger value='aliases'>Aliases</TabsTrigger>
                <TabsTrigger value='groups'>Groups</TabsTrigger>
                <TabsTrigger value='security'>Security</TabsTrigger>
                <TabsTrigger value='gmail'>Gmail</TabsTrigger>
                <TabsTrigger value='drive'>Drive</TabsTrigger>
              </TabsList>
              <TabsContent value='profile' className='pt-4'>
                <Card>
                  <CardHeader>
                    <CardTitle>Profile</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <dl className='grid grid-cols-1 gap-x-8 gap-y-3 text-sm sm:grid-cols-[max-content_1fr] lg:grid-cols-[max-content_1fr_max-content_1fr]'>
                      <Field label='Email' value={u.primaryEmail} />
                      <Field
                        label='Name'
                        value={`${u.givenName} ${u.familyName}`}
                      />
                      <Field label='Customer ID' value={u.customerId} />
                      <Field label='Organization unit' value={u.orgUnitPath} />
                      <Field
                        label='Status'
                        value={u.suspended ? 'Suspended' : 'Active'}
                      />
                      <Field
                        label='Administrator'
                        value={triState(u.superAdmin)}
                      />
                      <Field
                        label='Delegated administrator'
                        value={triState(u.delegatedAdmin)}
                      />
                      <Field
                        label='2-step enrolled / enforced'
                        value={`${triState(u.twoStepEnrolled)} / ${triState(u.twoStepEnforced)}`}
                      />
                      <Field
                        label='Last login'
                        value={lastLoginText(u.lastLoginTime)}
                      />
                      <Field
                        label='Password change required'
                        value={u.changePasswordAtNextLogin ? 'Yes' : 'No'}
                      />
                    </dl>
                  </CardContent>
                </Card>
              </TabsContent>
              <TabsContent value='aliases' className='pt-4'>
                <UserAliases
                  connectionId={connectionId}
                  tenantId={tenantId}
                  userId={userId}
                />
              </TabsContent>
              <TabsContent value='groups' className='pt-4'>
                <UserGroups
                  connectionId={connectionId}
                  tenantId={tenantId}
                  userId={userId}
                />
              </TabsContent>
              <TabsContent value='security' className='pt-4'>
                <UserSecurity
                  connectionId={connectionId}
                  tenantId={tenantId}
                  userId={userId}
                />
              </TabsContent>
              <TabsContent value='gmail' className='pt-4'>
                <UserGmail
                  connectionId={connectionId}
                  tenantId={tenantId}
                  userId={userId}
                  userEmail={u.primaryEmail}
                  confirmation={gmail.confirmation}
                  onConfirmation={gmail.setConfirmation}
                />
              </TabsContent>
              <TabsContent value='drive' className='pt-4'>
                <UserExposedFiles
                  connectionId={connectionId}
                  tenantId={tenantId}
                  userId={userId}
                  userEmail={u.primaryEmail}
                  confirmation={driveFiles.confirmation}
                  onConfirmation={driveFiles.setConfirmation}
                />
              </TabsContent>
            </Tabs>
          </>
        )}
      </PageScroll>
    </Page>
  )
}

function Summary({
  label,
  value,
  note,
}: {
  label: string
  value: string
  note?: string
}) {
  return (
    <Card className='gap-3 py-4'>
      <CardHeader className='px-4'>
        <CardDescription>{label}</CardDescription>
        <CardTitle className='text-2xl break-words'>{value}</CardTitle>
      </CardHeader>
      {note && (
        <CardContent className='px-4 text-xs text-muted-foreground'>
          {note}
        </CardContent>
      )}
    </Card>
  )
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className='text-muted-foreground'>{label}</dt>
      <dd className='break-all'>{value}</dd>
    </>
  )
}
