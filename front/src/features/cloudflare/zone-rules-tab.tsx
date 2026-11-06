import { CloudflareZoneRuleCategory, useCloudflareZoneRuleMutations, useCloudflareZoneRules } from '@/api/cloudflare'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

const labels: Record<number, string> = {
  [CloudflareZoneRuleCategory.SINGLE_REDIRECT]: 'Single redirects',
  [CloudflareZoneRuleCategory.URL_REWRITE]: 'URL rewrite rules',
  [CloudflareZoneRuleCategory.REQUEST_HEADER]: 'Request header transforms',
  [CloudflareZoneRuleCategory.RESPONSE_HEADER]: 'Response header transforms',
  [CloudflareZoneRuleCategory.ORIGIN]: 'Origin rules',
}

export function ZoneRulesTab({ connectionId, zoneId }: { connectionId: string; zoneId: string }) {
  const rules = useCloudflareZoneRules(connectionId, zoneId)
  const mutations = useCloudflareZoneRuleMutations(connectionId, zoneId)

  if (rules.isLoading) return <div className='space-y-3'><Skeleton className='h-24' /><Skeleton className='h-24' /></div>
  if (rules.error) return <Alert variant='destructive'><AlertTitle>Zone Rules could not be loaded</AlertTitle><AlertDescription>{rules.error.message}</AlertDescription></Alert>
  return <div className='grid gap-4 md:grid-cols-2'>
    {(rules.data ?? []).map((category) => <Card key={category.category}>
        <CardHeader><CardTitle>{labels[category.category] ?? 'Zone Rules'}</CardTitle><div className='flex gap-2'><button className='text-sm underline' onClick={() => { const description = window.prompt('Description', 'New rule'); if (description !== null) mutations.create.mutate({ connectionId, zoneId, category: category.category, rule: { description, expression: 'true', action: 'route' } }) }}>Create</button><button className='text-sm underline' onClick={() => { if (category.rules.length > 1) mutations.reorder.mutate({ connectionId, zoneId, category: category.category, snapshot: category.snapshot, ruleIds: [...category.rules].reverse().map(r => r.id) }) }}>Reverse order</button></div></CardHeader>
      <CardContent className='space-y-2'>
        {category.issue ? <Alert variant='destructive'><AlertDescription>{category.issue}</AlertDescription></Alert> : category.rules.length === 0 ? <p className='text-sm text-muted-foreground'>No rules configured.</p> : category.rules.map((rule, i) => <div key={rule.id || i} className='rounded border p-3 text-sm'>
          <div className='flex justify-between gap-2'><span className='font-medium'>{rule.description || rule.action}</span><span className='font-mono text-xs text-muted-foreground'>{rule.id}</span><span className='flex gap-2'><button className='underline' onClick={() => { const description = window.prompt('Description', rule.description); if (description !== null) mutations.update.mutate({ connectionId, zoneId, category: category.category, ruleId: rule.id, snapshot: category.snapshot, rule: { ...rule, description } }) }}>Edit</button><button className='underline' onClick={() => { if (window.confirm('Delete this rule?')) mutations.remove.mutate({ connectionId, zoneId, category: category.category, ruleId: rule.id }) }}>Delete</button></span></div>
          <div className='mt-1 font-mono text-xs'>{rule.expression}</div>
          {rule.readOnly && <div className='mt-1 text-xs text-muted-foreground'>Read-only: {rule.readOnlyReason}</div>}
        </div>)}
      </CardContent>
    </Card>)}
  </div>
}
