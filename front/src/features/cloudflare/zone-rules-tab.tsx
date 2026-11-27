import { useState } from 'react'
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  ChevronRight,
  CircleAlert,
  Pencil,
  Plus,
  RefreshCw,
  ShieldAlert,
  Trash2,
  TriangleAlert,
} from 'lucide-react'
import { toast } from 'sonner'
import {
  CloudflareResourceKind,
  useCloudflareZoneRuleChanges,
  useCloudflareZoneRulesCategory,
  type CloudflareZoneRule,
  type CloudflareZoneRuleCategory,
} from '@/api/cloudflare'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { ConfirmChangeDialog } from './manage-dialogs'
import { ResourceOperationsLog } from './resource-operations'
import { ZoneRuleDialog } from './zone-rule-dialog'
import {
  TRAFFIC_NOT_TESTED_NOTE,
  ZONE_RULE_CATEGORIES,
  canMoveRule,
  categoryIssueTitle,
  categoryMeta,
  describeRuleLabel,
  draftFromRule,
  moveRuleIds,
  ruleInputFromDraft,
  summarizeRuleAction,
} from './zone-rules'

/**
 * Zone Rules tab: manages Single Redirects, URL Rewrite Rules, Request Header
 * Transform Rules, Response Header Transform Rules and Origin Rules. Each
 * category loads and refreshes independently, followed by the Zone Rules
 * resource operation log.
 */
export function ZoneRulesTab({
  connectionId,
  zoneId,
  zoneName,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
}) {
  return (
    <div className='space-y-6'>
      <p className='text-sm text-muted-foreground'>
        Live Cloudflare HTTP rules for{' '}
        <span className='font-mono text-foreground'>{zoneName || zoneId}</span>.
        Each category loads independently and preserves Cloudflare&apos;s live
        evaluation order.
      </p>

      <div className='space-y-4'>
        {ZONE_RULE_CATEGORIES.map((meta) => (
          <CategorySection
            key={meta.category}
            connectionId={connectionId}
            zoneId={zoneId}
            zoneName={zoneName}
            category={meta.category}
          />
        ))}
      </div>

      <section className='space-y-2'>
        <h3 className='text-sm font-medium'>
          Zone Rules changes made from Neo Box
        </h3>
        <ResourceOperationsLog
          connectionId={connectionId}
          kind={CloudflareResourceKind.ZONE_RULES}
          resource={zoneName}
        />
      </section>
    </div>
  )
}

type PendingToggle = { rule: CloudflareZoneRule; nextEnabled: boolean }
type PendingReorder = {
  rule: CloudflareZoneRule
  index: number
  direction: 'up' | 'down'
  nextIds: string[]
}

function CategorySection({
  connectionId,
  zoneId,
  zoneName,
  category,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
  category: CloudflareZoneRuleCategory
}) {
  const meta = categoryMeta(category)
  const query = useCloudflareZoneRulesCategory(connectionId, zoneId, category)
  const changes = useCloudflareZoneRuleChanges(connectionId, zoneId, category)

  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<CloudflareZoneRule>()
  const [deleting, setDeleting] = useState<CloudflareZoneRule>()
  const [toggling, setToggling] = useState<PendingToggle>()
  const [reordering, setReordering] = useState<PendingReorder>()
  const [refreshWarning, setRefreshWarning] = useState<string>()
  const [expandedRaw, setExpandedRaw] = useState<Record<string, boolean>>({})

  const data = query.data
  const snapshot = data?.snapshot ?? ''
  const rules = data?.rules ?? []
  const hasCategoryError = Boolean(query.error || data?.issue)

  async function refreshAfterConfirmedChange() {
    setRefreshWarning(undefined)
    try {
      await changes.refreshCategory()
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setRefreshWarning(
        `Cloudflare accepted your change, but Neo Box could not refresh ${meta.label}: ${msg}`
      )
    }
  }

  return (
    <Card>
      <CardHeader className='flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between'>
        <div className='space-y-1'>
          <div className='flex flex-wrap items-center gap-2'>
            <CardTitle className='text-base'>{meta.label}</CardTitle>
            {data && !hasCategoryError && (
              <Badge variant='secondary'>
                {rules.length} {rules.length === 1 ? 'rule' : 'rules'}
              </Badge>
            )}
          </div>
          <CardDescription>{meta.description}</CardDescription>
        </div>
        <div className='flex shrink-0 gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={query.isFetching}
            onClick={() => {
              setRefreshWarning(undefined)
              void query.refetch()
            }}
          >
            <RefreshCw
              className={`h-3.5 w-3.5 ${query.isFetching ? 'animate-spin' : ''}`}
            />
            Refresh
          </Button>
          <Button
            size='sm'
            disabled={query.isLoading || hasCategoryError}
            title={
              hasCategoryError
                ? 'Resolve the read issue and refresh before adding rules.'
                : undefined
            }
            onClick={() => {
              setRefreshWarning(undefined)
              setCreating(true)
            }}
          >
            <Plus className='h-3.5 w-3.5' /> Add rule
          </Button>
        </div>
      </CardHeader>

      <CardContent className='space-y-3'>
        {refreshWarning && (
          <Alert>
            <TriangleAlert />
            <AlertTitle>Saved at Cloudflare, refresh needed</AlertTitle>
            <AlertDescription>{refreshWarning}</AlertDescription>
          </Alert>
        )}

        {query.isLoading ? (
          <div className='space-y-2'>
            <Skeleton className='h-14 w-full' />
            <Skeleton className='h-14 w-full' />
          </div>
        ) : query.error ? (
          <Alert variant='destructive'>
            <CircleAlert />
            <AlertTitle>Could not load {meta.label}</AlertTitle>
            <AlertDescription className='break-words'>
              {query.error.message}
            </AlertDescription>
          </Alert>
        ) : data?.issue ? (
          <Alert variant='destructive'>
            <ShieldAlert />
            <AlertTitle>{categoryIssueTitle(data.issueKind)}</AlertTitle>
            <AlertDescription className='space-y-1 break-words'>
              <p>{data.issue}</p>
              <p className='text-xs'>
                Required token permission: {meta.readPermission}
              </p>
            </AlertDescription>
          </Alert>
        ) : rules.length === 0 ? (
          <p className='text-sm text-muted-foreground'>
            {data?.rulesetMissing
              ? `No ${meta.label} ruleset has been initialized on this Zone yet. Adding the first rule will initialize it.`
              : `No rules configured in ${meta.label}.`}
          </p>
        ) : (
          <div className='space-y-2'>
            {rules.map((rule, index) => {
              const enabled = rule.enabled ?? true
              const moveUp = canMoveRule(rules, index, 'up')
              const moveDown = canMoveRule(rules, index, 'down')
              const rawKey = rule.id || String(index)
              const rawOpen = Boolean(expandedRaw[rawKey])

              return (
                <div
                  key={rawKey}
                  className='space-y-2 rounded-md border p-3 text-sm'
                >
                  <div className='flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between'>
                    <div className='min-w-0 space-y-1'>
                      <div className='flex flex-wrap items-center gap-2'>
                        <span className='font-mono text-xs text-muted-foreground'>
                          #{index + 1}
                        </span>
                        <span className='font-medium break-words'>
                          {rule.description || '(no description)'}
                        </span>
                        <Badge variant={enabled ? 'default' : 'secondary'}>
                          {enabled ? 'Enabled' : 'Disabled'}
                        </Badge>
                        {rule.readOnly && (
                          <Badge variant='outline'>Read-only</Badge>
                        )}
                      </div>
                      {rule.id && (
                        <div className='font-mono text-xs text-muted-foreground'>
                          ID: {rule.id}
                        </div>
                      )}
                    </div>

                    <div className='flex flex-wrap items-center gap-1'>
                      {!rule.readOnly && (
                        <>
                          <Button
                            size='sm'
                            variant='outline'
                            disabled={!moveUp.allowed}
                            title={moveUp.reason ?? 'Move up'}
                            aria-label={`Move rule #${index + 1} up`}
                            onClick={() => {
                              const nextIds = moveRuleIds(rules, index, 'up')
                              if (nextIds) {
                                setReordering({
                                  rule,
                                  index,
                                  direction: 'up',
                                  nextIds,
                                })
                              }
                            }}
                          >
                            <ArrowUp className='h-3.5 w-3.5' />
                          </Button>
                          <Button
                            size='sm'
                            variant='outline'
                            disabled={!moveDown.allowed}
                            title={moveDown.reason ?? 'Move down'}
                            aria-label={`Move rule #${index + 1} down`}
                            onClick={() => {
                              const nextIds = moveRuleIds(rules, index, 'down')
                              if (nextIds) {
                                setReordering({
                                  rule,
                                  index,
                                  direction: 'down',
                                  nextIds,
                                })
                              }
                            }}
                          >
                            <ArrowDown className='h-3.5 w-3.5' />
                          </Button>
                          <Button
                            size='sm'
                            variant='outline'
                            onClick={() =>
                              setToggling({ rule, nextEnabled: !enabled })
                            }
                          >
                            {enabled ? 'Disable' : 'Enable'}
                          </Button>
                          <Button
                            size='sm'
                            variant='outline'
                            aria-label={`Edit rule #${index + 1}`}
                            onClick={() => setEditing(rule)}
                          >
                            <Pencil className='h-3.5 w-3.5' />
                          </Button>
                          <Button
                            size='sm'
                            variant='outline'
                            aria-label={`Delete rule #${index + 1}`}
                            onClick={() => setDeleting(rule)}
                          >
                            <Trash2 className='h-3.5 w-3.5' />
                          </Button>
                        </>
                      )}
                    </div>
                  </div>

                  <div className='space-y-1 text-xs'>
                    <div className='flex flex-wrap gap-1.5'>
                      <span className='text-muted-foreground'>Match:</span>
                      <code className='font-mono break-all'>
                        {rule.expression}
                      </code>
                    </div>
                    <div className='flex flex-wrap gap-1.5'>
                      <span className='text-muted-foreground'>Action:</span>
                      <span className='break-words'>
                        {summarizeRuleAction(rule)}
                      </span>
                    </div>
                  </div>

                  {rule.readOnly && (
                    <div className='space-y-2 rounded-md bg-muted/40 p-2.5 text-xs'>
                      <div className='text-muted-foreground'>
                        Protected read-only: {rule.readOnlyReason}
                      </div>
                      {rule.rawConfigJson && (
                        <div>
                          <button
                            type='button'
                            className='inline-flex items-center gap-1 font-medium underline underline-offset-2'
                            onClick={() =>
                              setExpandedRaw((prev) => ({
                                ...prev,
                                [rawKey]: !prev[rawKey],
                              }))
                            }
                          >
                            {rawOpen ? (
                              <ChevronDown className='h-3.5 w-3.5' />
                            ) : (
                              <ChevronRight className='h-3.5 w-3.5' />
                            )}
                            {rawOpen
                              ? 'Hide original Cloudflare configuration'
                              : 'Show original Cloudflare configuration'}
                          </button>
                          {rawOpen && (
                            <pre className='mt-2 max-h-60 overflow-auto rounded border bg-background p-2 font-mono text-[11px]'>
                              {rule.rawConfigJson}
                            </pre>
                          )}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </CardContent>

      {creating && (
        <ZoneRuleDialog
          connectionId={connectionId}
          zoneId={zoneId}
          zoneName={zoneName}
          category={category}
          snapshot={snapshot}
          onConfirmedRefreshError={setRefreshWarning}
          onClose={() => setCreating(false)}
        />
      )}

      {editing && (
        <ZoneRuleDialog
          connectionId={connectionId}
          zoneId={zoneId}
          zoneName={zoneName}
          category={category}
          snapshot={snapshot}
          rule={editing}
          onConfirmedRefreshError={setRefreshWarning}
          onClose={() => setEditing(undefined)}
        />
      )}

      {toggling && (
        <ConfirmChangeDialog
          title={`${toggling.nextEnabled ? 'Enable' : 'Disable'} this rule?`}
          description={`Changes the enabled state in ${meta.label} at Cloudflare immediately. ${TRAFFIC_NOT_TESTED_NOTE}`}
          confirmLabel={toggling.nextEnabled ? 'Enable rule' : 'Disable rule'}
          pendingLabel='Saving…'
          destructive={false}
          what={meta.label}
          run={async () => {
            const draft = draftFromRule(category, toggling.rule, zoneName)
            draft.enabled = toggling.nextEnabled
            const res = await changes.update.mutateAsync({
              ruleId: toggling.rule.id,
              snapshot,
              rule: ruleInputFromDraft(category, draft),
            })
            if (res.result && !res.result.changed) {
              toast.info('Already as requested; nothing changed')
            } else {
              toast.success(
                toggling.nextEnabled
                  ? `Enabled rule in ${meta.label}`
                  : `Disabled rule in ${meta.label}`,
                { description: TRAFFIC_NOT_TESTED_NOTE }
              )
            }
            if (res.result?.operationLogError) {
              toast.warning(res.result.operationLogError, { duration: 10000 })
            }
            void refreshAfterConfirmedChange()
          }}
          onClose={() => setToggling(undefined)}
        >
          <dl className='grid grid-cols-[6rem_1fr] gap-x-3 gap-y-1 rounded-md border p-3 text-sm'>
            <dt className='text-muted-foreground'>Category</dt>
            <dd>{meta.label}</dd>
            <dt className='text-muted-foreground'>Rule</dt>
            <dd>{toggling.rule.description || toggling.rule.id}</dd>
            <dt className='text-muted-foreground'>Match</dt>
            <dd className='font-mono text-xs break-all'>
              {toggling.rule.expression}
            </dd>
            <dt className='text-muted-foreground'>State</dt>
            <dd>
              {toggling.nextEnabled
                ? 'Disabled → Enabled'
                : 'Enabled → Disabled'}
            </dd>
          </dl>
        </ConfirmChangeDialog>
      )}

      {deleting && (
        <ConfirmChangeDialog
          title={`Delete rule from ${meta.label}?`}
          description='This removes the rule from Cloudflare immediately. Untouched rules remain unchanged.'
          confirmLabel='Delete rule'
          pendingLabel='Deleting…'
          destructive
          what={meta.label}
          run={async () => {
            const res = await changes.remove.mutateAsync({
              ruleId: deleting.id,
              snapshot,
            })
            toast.success(`Deleted rule from ${meta.label}`)
            if (res.result?.operationLogError) {
              toast.warning(res.result.operationLogError, { duration: 10000 })
            }
            void refreshAfterConfirmedChange()
          }}
          onClose={() => setDeleting(undefined)}
        >
          <dl className='grid grid-cols-[6rem_1fr] gap-x-3 gap-y-1 rounded-md border p-3 text-sm'>
            <dt className='text-muted-foreground'>Category</dt>
            <dd>{meta.label}</dd>
            <dt className='text-muted-foreground'>Rule</dt>
            <dd>{deleting.description || '(no description)'}</dd>
            <dt className='text-muted-foreground'>ID</dt>
            <dd className='font-mono text-xs break-all'>{deleting.id}</dd>
            <dt className='text-muted-foreground'>Match</dt>
            <dd className='font-mono text-xs break-all'>
              {deleting.expression}
            </dd>
            <dt className='text-muted-foreground'>Action</dt>
            <dd>{summarizeRuleAction(deleting)}</dd>
          </dl>
        </ConfirmChangeDialog>
      )}

      {reordering && (
        <ConfirmChangeDialog
          title={`Reorder rules in ${meta.label}?`}
          description={`Moves this rule ${reordering.direction === 'up' ? 'earlier' : 'later'} in Cloudflare's evaluation order. ${TRAFFIC_NOT_TESTED_NOTE}`}
          confirmLabel='Save new order'
          pendingLabel='Reordering…'
          destructive={false}
          what={meta.label}
          run={async () => {
            const res = await changes.reorder.mutateAsync({
              snapshot,
              ruleIds: reordering.nextIds,
            })
            if (res.result && !res.result.changed) {
              toast.info('Order unchanged')
            } else {
              toast.success(`Updated order in ${meta.label}`, {
                description: TRAFFIC_NOT_TESTED_NOTE,
              })
            }
            if (res.result?.operationLogError) {
              toast.warning(res.result.operationLogError, { duration: 10000 })
            }
            void refreshAfterConfirmedChange()
          }}
          onClose={() => setReordering(undefined)}
        >
          <div className='space-y-2 rounded-md border p-3 text-sm'>
            <div className='text-xs text-muted-foreground'>
              {describeRuleLabel(reordering.rule, reordering.index)}: position #
              {reordering.index + 1} → #
              {reordering.direction === 'up'
                ? reordering.index
                : reordering.index + 2}
            </div>
            <ol className='space-y-1 text-xs'>
              {reordering.nextIds.map((id, i) => {
                const r = rules.find((x) => x.id === id)
                return (
                  <li
                    key={id}
                    className={
                      id === reordering.rule.id
                        ? 'font-medium text-foreground'
                        : 'text-muted-foreground'
                    }
                  >
                    #{i + 1} {r ? describeRuleLabel(r, i) : id}
                    {r?.readOnly ? ' (read-only)' : ''}
                  </li>
                )
              })}
            </ol>
          </div>
        </ConfirmChangeDialog>
      )}
    </Card>
  )
}
