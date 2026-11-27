import { useState, type FormEvent } from 'react'
import { CircleAlert, Plus, Trash2, TriangleAlert } from 'lucide-react'
import { toast } from 'sonner'
import {
  CloudflareHeaderOperation,
  CloudflareRedirectTargetKind,
  CloudflareRewritePartMode,
  CloudflareZoneRuleCategory,
  useCloudflareZoneRuleChanges,
  type CloudflareZoneRule,
} from '@/api/cloudflare'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Field } from './manage-dialogs'
import {
  ORIGIN_NO_DNS_NOTE,
  TRAFFIC_NOT_TESTED_NOTE,
  VALID_REDIRECT_STATUSES,
  buildCommonExpression,
  buildRuleConfirmationItems,
  canReturnToCommon,
  categoryMeta,
  defaultRuleDraft,
  draftFromRule,
  nextHeaderRowId,
  parseExpression,
  ruleInputFromDraft,
  validateRuleDraft,
  zoneRuleChangeError,
  type DraftErrors,
  type MatchMode,
  type PathMatchKind,
  type RuleDraft,
  type ZoneRuleErrorInfo,
} from './zone-rules'

/**
 * Two-step Create/Edit → Review & Confirm dialog for one Zone Rules HTTP rule.
 * Opening the editor or review step never writes. On validation, conflict or
 * API errors, all entered values are preserved so the user can fix or reconcile.
 */
export function ZoneRuleDialog({
  connectionId,
  zoneId,
  zoneName,
  category,
  snapshot,
  rule,
  onConfirmedRefreshError,
  onClose,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
  category: CloudflareZoneRuleCategory
  snapshot: string
  rule?: CloudflareZoneRule
  onConfirmedRefreshError: (message: string) => void
  onClose: () => void
}) {
  const mode: 'create' | 'edit' = rule ? 'edit' : 'create'
  const meta = categoryMeta(category)
  const changes = useCloudflareZoneRuleChanges(connectionId, zoneId, category)

  const [draft, setDraft] = useState<RuleDraft>(() =>
    rule
      ? draftFromRule(category, rule, zoneName)
      : defaultRuleDraft(category, zoneName)
  )
  const [step, setStep] = useState<'edit' | 'confirm'>('edit')
  const [errors, setErrors] = useState<DraftErrors>({})
  const [failure, setFailure] = useState<ZoneRuleErrorInfo>()
  const [pending, setPending] = useState(false)

  const canSwitchToCommon =
    draft.matchMode === 'common' ||
    draft.matchMode === 'all' ||
    canReturnToCommon(draft.expression)

  function switchMatchMode(next: MatchMode) {
    setErrors((e) => {
      const copy = { ...e }
      delete copy.matchHost
      delete copy.matchPath
      delete copy.expression
      return copy
    })
    if (next === draft.matchMode) return
    if (next === 'advanced') {
      const expr =
        draft.matchMode === 'all'
          ? 'true'
          : draft.common.host.trim()
            ? buildCommonExpression(draft.common)
            : draft.expression
      setDraft((d) => ({ ...d, matchMode: 'advanced', expression: expr }))
      return
    }
    if (next === 'all') {
      setDraft((d) => ({ ...d, matchMode: 'all', expression: 'true' }))
      return
    }
    // Switching back to 'common': only allowed if expression preserves meaning
    // (or coming from explicit 'all').
    if (draft.matchMode === 'advanced') {
      const parsed = parseExpression(draft.expression)
      if (parsed.mode !== 'common' || !parsed.common) return
      setDraft((d) => ({
        ...d,
        matchMode: 'common',
        common: parsed.common!,
      }))
      return
    }
    setDraft((d) => ({ ...d, matchMode: 'common' }))
  }

  function handleReview(e: FormEvent) {
    e.preventDefault()
    const found = validateRuleDraft(category, draft)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    setFailure(undefined)
    setStep('confirm')
  }

  async function handleConfirmSubmit() {
    if (pending) return
    setFailure(undefined)
    setPending(true)
    const input = ruleInputFromDraft(category, draft)
    try {
      const res =
        mode === 'create'
          ? await changes.create.mutateAsync({ snapshot, rule: input })
          : await changes.update.mutateAsync({
              ruleId: rule!.id,
              snapshot,
              rule: input,
            })

      const result = res.result
      if (result && !result.changed) {
        toast.info('Already as requested; nothing changed')
      } else {
        toast.success(
          mode === 'create'
            ? `Added rule in ${meta.label}`
            : `Updated rule in ${meta.label}`,
          { description: TRAFFIC_NOT_TESTED_NOTE }
        )
      }
      if (result?.operationLogError) {
        toast.warning(result.operationLogError, { duration: 10000 })
      }
      onClose()

      // Refresh affected category separately: a refresh failure never turns a
      // confirmed write into a failed write or invites resubmission.
      try {
        await changes.refreshCategory()
      } catch (refreshErr) {
        const msg =
          refreshErr instanceof Error ? refreshErr.message : String(refreshErr)
        onConfirmedRefreshError(
          `Cloudflare accepted your change, but Neo Box could not refresh ${meta.label}: ${msg}`
        )
      }
    } catch (err) {
      setFailure(zoneRuleChangeError(err, category))
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>
            {step === 'confirm'
              ? `Confirm ${mode === 'create' ? 'new' : 'updated'} rule`
              : mode === 'create'
                ? `Add rule · ${meta.label}`
                : `Edit rule · ${meta.label}`}
          </DialogTitle>
          <DialogDescription>
            {step === 'confirm'
              ? 'Review the exact match, behavior and state before sending to Cloudflare.'
              : meta.description}
          </DialogDescription>
        </DialogHeader>

        {step === 'edit' ? (
          <form onSubmit={handleReview} className='space-y-4'>
            <Field
              label='Description'
              id='rule-description'
              error={errors.description}
              hint='Optional label to recognize this rule.'
            >
              <Input
                id='rule-description'
                value={draft.description}
                onChange={(e) =>
                  setDraft((d) => ({ ...d, description: e.target.value }))
                }
                placeholder='e.g. Redirect legacy blog path'
                autoComplete='off'
              />
            </Field>

            <div className='flex items-center justify-between rounded-md border p-3'>
              <div>
                <Label htmlFor='rule-enabled'>Rule enabled</Label>
                <p className='text-xs text-muted-foreground'>
                  {mode === 'create'
                    ? 'New rules default to enabled; turn off to create disabled.'
                    : 'Disabled rules stay in Cloudflare without matching traffic.'}
                </p>
              </div>
              <Switch
                id='rule-enabled'
                checked={draft.enabled}
                onCheckedChange={(v) => setDraft((d) => ({ ...d, enabled: v }))}
              />
            </div>

            <div className='space-y-3 rounded-md border p-3'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <Label>When incoming requests match</Label>
                <div className='flex gap-1'>
                  <Button
                    type='button'
                    size='sm'
                    variant={
                      draft.matchMode === 'common' ? 'default' : 'outline'
                    }
                    disabled={!canSwitchToCommon}
                    title={
                      canSwitchToCommon
                        ? undefined
                        : 'This expression cannot be represented in the common form without changing its meaning.'
                    }
                    onClick={() => switchMatchMode('common')}
                  >
                    Common form
                  </Button>
                  <Button
                    type='button'
                    size='sm'
                    variant={draft.matchMode === 'all' ? 'default' : 'outline'}
                    onClick={() => switchMatchMode('all')}
                  >
                    Match all requests
                  </Button>
                  <Button
                    type='button'
                    size='sm'
                    variant={
                      draft.matchMode === 'advanced' ? 'default' : 'outline'
                    }
                    onClick={() => switchMatchMode('advanced')}
                  >
                    Advanced expression
                  </Button>
                </div>
              </div>

              {draft.matchMode === 'common' && (
                <div className='space-y-3'>
                  <Field
                    label='Exact hostname'
                    id='match-host'
                    error={errors.matchHost}
                    hint={
                      zoneName
                        ? `Requests to this hostname in ${zoneName}.`
                        : undefined
                    }
                  >
                    <Input
                      id='match-host'
                      value={draft.common.host}
                      onChange={(e) =>
                        setDraft((d) => ({
                          ...d,
                          common: { ...d.common, host: e.target.value },
                        }))
                      }
                      placeholder={zoneName || 'app.example.com'}
                      className='font-mono'
                      autoComplete='off'
                    />
                  </Field>
                  <div className='grid gap-3 sm:grid-cols-2'>
                    <Field
                      label='Path condition (optional)'
                      id='match-path-kind'
                    >
                      <Select
                        value={draft.common.pathKind}
                        onValueChange={(v) =>
                          setDraft((d) => ({
                            ...d,
                            common: {
                              ...d.common,
                              pathKind: v as PathMatchKind,
                            },
                          }))
                        }
                      >
                        <SelectTrigger id='match-path-kind'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value='none'>Any path</SelectItem>
                          <SelectItem value='exact'>Exact path</SelectItem>
                          <SelectItem value='prefix'>
                            Path starts with
                          </SelectItem>
                        </SelectContent>
                      </Select>
                    </Field>
                    {draft.common.pathKind !== 'none' && (
                      <Field
                        label='Path'
                        id='match-path'
                        error={errors.matchPath}
                      >
                        <Input
                          id='match-path'
                          value={draft.common.path}
                          onChange={(e) =>
                            setDraft((d) => ({
                              ...d,
                              common: { ...d.common, path: e.target.value },
                            }))
                          }
                          placeholder={
                            draft.common.pathKind === 'prefix'
                              ? '/api/'
                              : '/old-page'
                          }
                          className='font-mono'
                          autoComplete='off'
                        />
                      </Field>
                    )}
                  </div>
                </div>
              )}

              {draft.matchMode === 'all' && (
                <Alert>
                  <AlertTitle>Matches all requests in this Zone</AlertTitle>
                  <AlertDescription>
                    Explicitly sets the expression to{' '}
                    <code className='font-mono'>true</code> so every HTTP
                    request to this Zone matches this rule.
                  </AlertDescription>
                </Alert>
              )}

              {draft.matchMode === 'advanced' && (
                <Field
                  label='Cloudflare expression'
                  id='match-expression'
                  error={errors.expression}
                  hint={
                    canSwitchToCommon
                      ? 'Cloudflare Ruleset expression syntax.'
                      : 'This expression uses conditions outside the common form, so it stays in advanced mode to preserve its exact meaning.'
                  }
                >
                  <Textarea
                    id='match-expression'
                    value={draft.expression}
                    onChange={(e) =>
                      setDraft((d) => ({ ...d, expression: e.target.value }))
                    }
                    placeholder='(http.host eq "app.example.com" and ip.src.country eq "US")'
                    className='font-mono text-xs'
                    rows={3}
                  />
                </Field>
              )}
            </div>

            <CategoryActionEditor
              category={category}
              draft={draft}
              setDraft={setDraft}
              errors={errors}
            />

            <DialogFooter>
              <Button type='button' variant='outline' onClick={onClose}>
                Cancel
              </Button>
              <Button type='submit'>Review change</Button>
            </DialogFooter>
          </form>
        ) : (
          <div className='space-y-4'>
            <dl className='grid grid-cols-[7.5rem_1fr] gap-x-3 gap-y-2 rounded-md border p-3 text-sm'>
              {buildRuleConfirmationItems(category, draft, mode, rule).map(
                (item) => (
                  <div key={item.label} className='contents'>
                    <dt className='text-muted-foreground'>{item.label}</dt>
                    <dd
                      className={
                        item.mono
                          ? 'font-mono text-xs break-all'
                          : 'break-words'
                      }
                    >
                      {item.value}
                    </dd>
                  </div>
                )
              )}
            </dl>

            <p className='text-xs text-muted-foreground'>
              {TRAFFIC_NOT_TESTED_NOTE}
              {category === CloudflareZoneRuleCategory.ORIGIN
                ? ` ${ORIGIN_NO_DNS_NOTE}`
                : ''}
            </p>

            {failure && (
              <Alert variant={failure.uncertain ? 'default' : 'destructive'}>
                {failure.uncertain ? <TriangleAlert /> : <CircleAlert />}
                <AlertTitle>{failure.title}</AlertTitle>
                <AlertDescription className='space-y-2 break-words'>
                  <p>{failure.message}</p>
                  {failure.kind === 'conflict' && (
                    <p className='text-xs'>
                      Go back to keep your input while refreshing the category,
                      or cancel and re-open after reviewing external changes.
                    </p>
                  )}
                </AlertDescription>
              </Alert>
            )}

            <DialogFooter className='gap-2 sm:gap-0'>
              <Button
                type='button'
                variant='outline'
                disabled={pending}
                onClick={() => setStep('edit')}
              >
                Back to edit
              </Button>
              {failure?.kind === 'conflict' && (
                <Button
                  type='button'
                  variant='secondary'
                  disabled={pending}
                  onClick={async () => {
                    try {
                      await changes.refreshCategory()
                      toast.info(
                        `Refreshed ${meta.label}. Close this dialog or copy your values to reconcile.`
                      )
                    } catch {
                      toast.error(`Could not refresh ${meta.label}`)
                    }
                  }}
                >
                  Refresh category
                </Button>
              )}
              <Button
                type='button'
                disabled={pending}
                onClick={handleConfirmSubmit}
              >
                {pending
                  ? 'Saving…'
                  : mode === 'create'
                    ? 'Confirm & create rule'
                    : 'Confirm & save changes'}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

function CategoryActionEditor({
  category,
  draft,
  setDraft,
  errors,
}: {
  category: CloudflareZoneRuleCategory
  draft: RuleDraft
  setDraft: React.Dispatch<React.SetStateAction<RuleDraft>>
  errors: DraftErrors
}) {
  switch (category) {
    case CloudflareZoneRuleCategory.SINGLE_REDIRECT:
      return (
        <div className='space-y-3 rounded-md border p-3'>
          <Label>Redirect behavior</Label>
          <div className='grid gap-3 sm:grid-cols-2'>
            <Field label='Target type' id='redir-kind'>
              <Select
                value={String(draft.redirectTargetKind)}
                onValueChange={(v) =>
                  setDraft((d) => ({
                    ...d,
                    redirectTargetKind: Number(
                      v
                    ) as CloudflareRedirectTargetKind,
                  }))
                }
              >
                <SelectTrigger id='redir-kind'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem
                    value={String(CloudflareRedirectTargetKind.STATIC)}
                  >
                    Static URL
                  </SelectItem>
                  <SelectItem
                    value={String(CloudflareRedirectTargetKind.EXPRESSION)}
                  >
                    Dynamic expression
                  </SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field
              label='Status code'
              id='redir-status'
              error={errors.redirectStatus}
            >
              <Select
                value={String(draft.redirectStatusCode)}
                onValueChange={(v) =>
                  setDraft((d) => ({ ...d, redirectStatusCode: Number(v) }))
                }
              >
                <SelectTrigger id='redir-status'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {VALID_REDIRECT_STATUSES.map((code) => (
                    <SelectItem key={code} value={String(code)}>
                      {code}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
          <Field
            label={
              draft.redirectTargetKind ===
              CloudflareRedirectTargetKind.EXPRESSION
                ? 'Target URL expression'
                : 'Target URL'
            }
            id='redir-target'
            error={errors.redirectTarget}
          >
            <Input
              id='redir-target'
              value={draft.redirectTargetValue}
              onChange={(e) =>
                setDraft((d) => ({ ...d, redirectTargetValue: e.target.value }))
              }
              placeholder={
                draft.redirectTargetKind ===
                CloudflareRedirectTargetKind.EXPRESSION
                  ? 'concat("https://example.com", http.request.uri.path)'
                  : 'https://example.com/new-path'
              }
              className='font-mono'
              autoComplete='off'
            />
          </Field>
          <div className='flex items-center gap-2'>
            <Checkbox
              id='redir-preserve-qs'
              checked={draft.redirectPreserveQuery}
              onCheckedChange={(v) =>
                setDraft((d) => ({ ...d, redirectPreserveQuery: v === true }))
              }
            />
            <Label htmlFor='redir-preserve-qs'>Preserve query string</Label>
          </div>
        </div>
      )

    case CloudflareZoneRuleCategory.URL_REWRITE:
      return (
        <div className='space-y-3 rounded-md border p-3'>
          <Label>URL rewrite behavior</Label>
          {errors.urlRewrite && (
            <p className='text-xs text-danger-foreground'>
              {errors.urlRewrite}
            </p>
          )}
          <div className='grid gap-3 sm:grid-cols-2'>
            <Field label='Path' id='rw-path-mode'>
              <Select
                value={String(draft.rewritePathMode)}
                onValueChange={(v) =>
                  setDraft((d) => ({
                    ...d,
                    rewritePathMode: Number(v) as CloudflareRewritePartMode,
                  }))
                }
              >
                <SelectTrigger id='rw-path-mode'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={String(CloudflareRewritePartMode.KEEP)}>
                    Preserve original
                  </SelectItem>
                  <SelectItem value={String(CloudflareRewritePartMode.STATIC)}>
                    Rewrite to static value
                  </SelectItem>
                  <SelectItem
                    value={String(CloudflareRewritePartMode.EXPRESSION)}
                  >
                    Rewrite dynamically (expression)
                  </SelectItem>
                </SelectContent>
              </Select>
            </Field>
            {draft.rewritePathMode !== CloudflareRewritePartMode.KEEP && (
              <Field
                label={
                  draft.rewritePathMode === CloudflareRewritePartMode.EXPRESSION
                    ? 'Path expression'
                    : 'Static path'
                }
                id='rw-path-val'
                error={errors.rewritePath}
              >
                <Input
                  id='rw-path-val'
                  value={draft.rewritePathValue}
                  onChange={(e) =>
                    setDraft((d) => ({
                      ...d,
                      rewritePathValue: e.target.value,
                    }))
                  }
                  placeholder={
                    draft.rewritePathMode ===
                    CloudflareRewritePartMode.EXPRESSION
                      ? 'concat("/v2", http.request.uri.path)'
                      : '/new-path'
                  }
                  className='font-mono'
                  autoComplete='off'
                />
              </Field>
            )}
          </div>
          <div className='grid gap-3 sm:grid-cols-2'>
            <Field label='Query string' id='rw-query-mode'>
              <Select
                value={String(draft.rewriteQueryMode)}
                onValueChange={(v) =>
                  setDraft((d) => ({
                    ...d,
                    rewriteQueryMode: Number(v) as CloudflareRewritePartMode,
                  }))
                }
              >
                <SelectTrigger id='rw-query-mode'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={String(CloudflareRewritePartMode.KEEP)}>
                    Preserve original
                  </SelectItem>
                  <SelectItem value={String(CloudflareRewritePartMode.STATIC)}>
                    Rewrite to static value
                  </SelectItem>
                  <SelectItem
                    value={String(CloudflareRewritePartMode.EXPRESSION)}
                  >
                    Rewrite dynamically (expression)
                  </SelectItem>
                </SelectContent>
              </Select>
            </Field>
            {draft.rewriteQueryMode !== CloudflareRewritePartMode.KEEP && (
              <Field
                label={
                  draft.rewriteQueryMode ===
                  CloudflareRewritePartMode.EXPRESSION
                    ? 'Query expression'
                    : 'Static query (blank strips query)'
                }
                id='rw-query-val'
                error={errors.rewriteQuery}
              >
                <Input
                  id='rw-query-val'
                  value={draft.rewriteQueryValue}
                  onChange={(e) =>
                    setDraft((d) => ({
                      ...d,
                      rewriteQueryValue: e.target.value,
                    }))
                  }
                  placeholder={
                    draft.rewriteQueryMode ===
                    CloudflareRewritePartMode.EXPRESSION
                      ? 'http.request.uri.query'
                      : 'source=edge'
                  }
                  className='font-mono'
                  autoComplete='off'
                />
              </Field>
            )}
          </div>
        </div>
      )

    case CloudflareZoneRuleCategory.REQUEST_HEADER:
    case CloudflareZoneRuleCategory.RESPONSE_HEADER: {
      const allowAdd = category === CloudflareZoneRuleCategory.RESPONSE_HEADER
      return (
        <div className='space-y-3 rounded-md border p-3'>
          <div className='flex items-center justify-between'>
            <Label>Header modifications</Label>
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() =>
                setDraft((d) => ({
                  ...d,
                  headers: [
                    ...d.headers,
                    {
                      id: nextHeaderRowId(),
                      name: '',
                      operation: CloudflareHeaderOperation.SET_STATIC,
                      value: '',
                    },
                  ],
                }))
              }
            >
              <Plus className='h-3.5 w-3.5' /> Add header
            </Button>
          </div>
          {errors.headers && (
            <p className='text-xs text-danger-foreground'>{errors.headers}</p>
          )}
          <div className='space-y-3'>
            {draft.headers.map((h, i) => (
              <div
                key={h.id}
                className='space-y-2 rounded-md border bg-muted/20 p-2.5'
              >
                <div className='grid gap-2 sm:grid-cols-[1fr_11rem_auto]'>
                  <Field
                    label='Header name'
                    id={`hdr-name-${h.id}`}
                    error={errors[`header_${i}_name`]}
                  >
                    <Input
                      id={`hdr-name-${h.id}`}
                      value={h.name}
                      onChange={(e) =>
                        setDraft((d) => ({
                          ...d,
                          headers: d.headers.map((row) =>
                            row.id === h.id
                              ? { ...row, name: e.target.value }
                              : row
                          ),
                        }))
                      }
                      placeholder='X-Custom-Header'
                      className='font-mono'
                      autoComplete='off'
                    />
                  </Field>
                  <Field
                    label='Operation'
                    id={`hdr-op-${h.id}`}
                    error={errors[`header_${i}_op`]}
                  >
                    <Select
                      value={String(h.operation)}
                      onValueChange={(v) =>
                        setDraft((d) => ({
                          ...d,
                          headers: d.headers.map((row) =>
                            row.id === h.id
                              ? {
                                  ...row,
                                  operation: Number(
                                    v
                                  ) as CloudflareHeaderOperation,
                                }
                              : row
                          ),
                        }))
                      }
                    >
                      <SelectTrigger id={`hdr-op-${h.id}`}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem
                          value={String(CloudflareHeaderOperation.SET_STATIC)}
                        >
                          Set static
                        </SelectItem>
                        <SelectItem
                          value={String(
                            CloudflareHeaderOperation.SET_EXPRESSION
                          )}
                        >
                          Set expression
                        </SelectItem>
                        {allowAdd && (
                          <>
                            <SelectItem
                              value={String(
                                CloudflareHeaderOperation.ADD_STATIC
                              )}
                            >
                              Add static
                            </SelectItem>
                            <SelectItem
                              value={String(
                                CloudflareHeaderOperation.ADD_EXPRESSION
                              )}
                            >
                              Add expression
                            </SelectItem>
                          </>
                        )}
                        <SelectItem
                          value={String(CloudflareHeaderOperation.REMOVE)}
                        >
                          Remove
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </Field>
                  <div className='flex items-end'>
                    <Button
                      type='button'
                      size='sm'
                      variant='outline'
                      disabled={draft.headers.length <= 1}
                      aria-label={`Remove header row ${i + 1}`}
                      onClick={() =>
                        setDraft((d) => ({
                          ...d,
                          headers: d.headers.filter((row) => row.id !== h.id),
                        }))
                      }
                    >
                      <Trash2 className='h-3.5 w-3.5' />
                    </Button>
                  </div>
                </div>
                {h.operation !== CloudflareHeaderOperation.REMOVE && (
                  <Field
                    label={
                      h.operation ===
                        CloudflareHeaderOperation.SET_EXPRESSION ||
                      h.operation === CloudflareHeaderOperation.ADD_EXPRESSION
                        ? 'Expression'
                        : 'Static value'
                    }
                    id={`hdr-val-${h.id}`}
                    error={errors[`header_${i}_value`]}
                  >
                    <Input
                      id={`hdr-val-${h.id}`}
                      value={h.value}
                      onChange={(e) =>
                        setDraft((d) => ({
                          ...d,
                          headers: d.headers.map((row) =>
                            row.id === h.id
                              ? { ...row, value: e.target.value }
                              : row
                          ),
                        }))
                      }
                      placeholder={
                        h.operation ===
                          CloudflareHeaderOperation.SET_EXPRESSION ||
                        h.operation === CloudflareHeaderOperation.ADD_EXPRESSION
                          ? 'ip.src.country'
                          : 'value'
                      }
                      className='font-mono'
                      autoComplete='off'
                    />
                  </Field>
                )}
              </div>
            ))}
          </div>
          <p className='text-xs text-muted-foreground'>
            Header values are sent directly to Cloudflare and are never stored
            in Neo Box&apos;s operation log.
          </p>
        </div>
      )
    }

    case CloudflareZoneRuleCategory.ORIGIN:
      return (
        <div className='space-y-3 rounded-md border p-3'>
          <Label>Origin overrides</Label>
          {errors.origin && (
            <p className='text-xs text-danger-foreground'>{errors.origin}</p>
          )}
          <div className='grid gap-3 sm:grid-cols-2'>
            <Field
              label='Destination port'
              id='origin-port'
              error={errors.originPort}
              hint='Optional, 1–65535.'
            >
              <Input
                id='origin-port'
                value={draft.originPort}
                onChange={(e) =>
                  setDraft((d) => ({ ...d, originPort: e.target.value }))
                }
                placeholder='8443'
                className='font-mono'
                autoComplete='off'
              />
            </Field>
            <Field
              label='Host header'
              id='origin-host'
              error={errors.originHostHeader}
              hint='Optional HTTP Host header sent to origin.'
            >
              <Input
                id='origin-host'
                value={draft.originHostHeader}
                onChange={(e) =>
                  setDraft((d) => ({ ...d, originHostHeader: e.target.value }))
                }
                placeholder='internal.example.com'
                className='font-mono'
                autoComplete='off'
              />
            </Field>
            <Field
              label='TLS SNI'
              id='origin-sni'
              error={errors.originSni}
              hint='Optional Server Name Indication value.'
            >
              <Input
                id='origin-sni'
                value={draft.originSni}
                onChange={(e) =>
                  setDraft((d) => ({ ...d, originSni: e.target.value }))
                }
                placeholder='origin-tls.example.com'
                className='font-mono'
                autoComplete='off'
              />
            </Field>
            <Field
              label='DNS resolution target'
              id='origin-dns'
              error={errors.originDnsRecord}
              hint={ORIGIN_NO_DNS_NOTE}
            >
              <Input
                id='origin-dns'
                value={draft.originDnsRecord}
                onChange={(e) =>
                  setDraft((d) => ({ ...d, originDnsRecord: e.target.value }))
                }
                placeholder='origin.example.com'
                className='font-mono'
                autoComplete='off'
              />
            </Field>
          </div>
        </div>
      )

    default:
      return null
  }
}
