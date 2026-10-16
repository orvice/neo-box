import { useState } from 'react'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import {
  CloudflareEmailAction as Action,
  useCloudflareEmailChanges,
  type CloudflareEmailRule,
} from '@/api/cloudflare'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { DataTable, type Column } from '@/components/data-table'
import { toastChange, toastFailure } from './changed'
import { ruleAddressProblem, ruleSummary, targetProblem } from './email'
import { ConfirmChangeDialog, Field, FormDialog } from './manage-dialogs'

/** A Zone's routing rules: switch, edit and delete the ones that can be. */
export function RulesSection({
  connectionId,
  zoneId,
  zoneName,
  rules,
  verified,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
  rules: CloudflareEmailRule[]
  /** The verified destination addresses, for the form's hint. */
  verified: string[]
}) {
  const changes = useCloudflareEmailChanges(connectionId, zoneId)
  const [editing, setEditing] = useState<CloudflareEmailRule | 'new'>()
  const [deleting, setDeleting] = useState<CloudflareEmailRule>()

  function toggle(r: CloudflareEmailRule, enabled: boolean) {
    changes.updateRule.mutate(
      {
        ruleId: r.id,
        rule: {
          name: r.name,
          address: r.address,
          action: r.action,
          target: r.target,
          enabled,
        },
      },
      {
        onSuccess: (res) =>
          toastChange(
            enabled ? `${r.address} is on` : `${r.address} is off`,
            res.result
          ),
        onError: (err) => toastFailure(err, 'routing rules'),
      }
    )
  }

  const busy = (r: CloudflareEmailRule) =>
    changes.updateRule.isPending &&
    changes.updateRule.variables?.ruleId === r.id

  const columns: Column<CloudflareEmailRule>[] = [
    {
      header: 'Custom address',
      cell: (r) => (
        <div>
          <div className='font-mono text-xs'>{r.address}</div>
          {r.name && (
            <div className='text-xs text-muted-foreground'>{r.name}</div>
          )}
        </div>
      ),
    },
    {
      header: 'Action',
      cell: (r) => <span className='text-sm'>{ruleSummary(r)}</span>,
    },
    {
      header: 'On',
      cell: (r) => (
        <Switch
          checked={r.enabled}
          disabled={!r.editable || busy(r)}
          onCheckedChange={(v) => toggle(r, v)}
          aria-label={`Rule for ${r.address}`}
        />
      ),
    },
    {
      header: 'Actions',
      cell: (r) =>
        r.editable ? (
          <div className='flex gap-1'>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setEditing(r)}
              aria-label={`Edit the rule for ${r.address}`}
            >
              <Pencil className='h-3.5 w-3.5' />
            </Button>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setDeleting(r)}
              aria-label={`Delete the rule for ${r.address}`}
            >
              <Trash2 className='h-3.5 w-3.5' />
            </Button>
          </div>
        ) : (
          <span
            className='text-xs text-muted-foreground'
            title={r.readonlyReason}
          >
            {r.readonlyReason || 'Read-only'}
          </span>
        ),
    },
  ]

  return (
    <section className='space-y-2'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h3 className='text-sm font-medium'>Routing rules</h3>
        <Button size='sm' onClick={() => setEditing('new')}>
          <Plus className='h-4 w-4' /> Add rule
        </Button>
      </div>
      <DataTable
        columns={columns}
        data={rules}
        isLoading={false}
        emptyMessage='No routing rules'
      />
      {editing && (
        <RuleDialog
          connectionId={connectionId}
          zoneId={zoneId}
          zoneName={zoneName}
          rule={editing === 'new' ? undefined : editing}
          verified={verified}
          onClose={() => setEditing(undefined)}
        />
      )}
      {deleting && (
        <ConfirmChangeDialog
          title='Delete this routing rule?'
          description={
            <>
              Mail to <span className='font-mono'>{deleting.address}</span> is
              no longer routed by a rule.
            </>
          }
          confirmLabel='Delete rule'
          pendingLabel='Deleting…'
          what='routing rules'
          run={async () => {
            const res = await changes.deleteRule.mutateAsync(deleting.id)
            toastChange(`Deleted the rule for ${deleting.address}`, res.result)
          }}
          onClose={() => setDeleting(undefined)}
        />
      )}
    </section>
  )
}

/** Adds a rule, or changes one. The action is chosen in the form's body. */
function RuleDialog({
  connectionId,
  zoneId,
  zoneName,
  rule,
  verified,
  onClose,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
  rule?: CloudflareEmailRule
  verified: string[]
  onClose: () => void
}) {
  const changes = useCloudflareEmailChanges(connectionId, zoneId)
  const [action, setAction] = useState<Action>(
    rule?.action === Action.DROP ? Action.DROP : Action.FORWARD
  )
  const forward = action === Action.FORWARD
  return (
    <FormDialog
      title={rule ? 'Edit routing rule' : 'Add a routing rule'}
      description={`Mail sent to the custom address at ${zoneName} is handled by this rule.`}
      submitLabel={rule ? 'Save rule' : 'Add rule'}
      what='routing rules'
      fields={[
        {
          name: 'address',
          label: 'Custom address',
          placeholder: `info@${zoneName}`,
          initial: rule?.address,
          mono: true,
          check: (v) => ruleAddressProblem(v, zoneName),
        },
        {
          name: 'target',
          label: 'Destination address',
          initial: rule?.action === Action.FORWARD ? rule.target : '',
          mono: true,
          disabled: !forward,
          hint: forward
            ? verified.length > 0
              ? `Verified: ${verified.join(', ')}`
              : 'No verified destination address yet; add one below first.'
            : 'A dropped message is discarded, so there is no destination.',
          check: (v) => targetProblem(action, v),
        },
        {
          name: 'name',
          label: 'Name (optional)',
          initial: rule?.name,
        },
      ]}
      toggles={[
        { name: 'enabled', label: 'On', initial: rule?.enabled ?? true },
      ]}
      run={async (v) => {
        const input = {
          name: v.text.name,
          address: v.text.address,
          action,
          target: forward ? v.text.target : '',
          enabled: v.on.enabled,
        }
        const res = rule
          ? await changes.updateRule.mutateAsync({
              ruleId: rule.id,
              rule: input,
            })
          : await changes.createRule.mutateAsync(input)
        toastChange(rule ? 'Rule saved' : 'Rule added', res.result)
      }}
      onClose={onClose}
    >
      <Field label='Action' id='rule-action'>
        <Select
          value={String(action)}
          onValueChange={(v) => setAction(Number(v) as Action)}
        >
          <SelectTrigger id='rule-action' className='w-full'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={String(Action.FORWARD)}>
              Forward to a destination address
            </SelectItem>
            <SelectItem value={String(Action.DROP)}>Drop</SelectItem>
          </SelectContent>
        </Select>
      </Field>
    </FormDialog>
  )
}
