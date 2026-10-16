import type { JsonObject } from '@bufbuild/protobuf'
import {
  CloudflarePagesEnvironment as Env,
  CloudflareTunnelRecordStatus as RecordStatus,
  type CloudflarePagesDeployment,
  type CloudflareResourceOperation,
  type CloudflareTunnelRecordResult,
  type CloudflareWorkerDeployment,
} from '@/api/cloudflare'

// Light checks that save a round trip; the backend and Cloudflare check
// again and have the last word.

const LABEL = /^[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?$/
const CRON_FIELD = /^[0-9A-Za-z*/,\-?#]+$/
const BINDING = /^[A-Za-z_][A-Za-z0-9_]*$/
const PROJECT = /^[a-z0-9][a-z0-9-]*$/
const BRANCH = /^[A-Za-z0-9._/-]+$/
const MAX_SECRET_BYTES = 5 * 1024

/** The problem with a custom domain, or undefined when it looks right. */
export function domainProblem(input: string): string | undefined {
  const h = input.trim().toLowerCase()
  if (!h) return 'Enter the hostname.'
  if (h.includes('*')) return 'A wildcard cannot be a custom domain.'
  const labels = h.split('.')
  if (labels.length < 2 || !labels.every((l) => LABEL.test(l)))
    return 'Use a domain name such as app.example.com, without a scheme, port or path.'
}

/** The problem with a Worker route pattern, or undefined. */
export function routeProblem(input: string): string | undefined {
  const p = input.trim()
  if (!p) return 'Enter the route pattern.'
  if (p.length > 1000 || /\s/.test(p))
    return 'A route pattern such as example.com/* has no spaces.'
  if (p.includes('://'))
    return 'Leave out the scheme: example.com/*, not https://example.com/*.'
  const host = p.split('/')[0]
  if (!p.includes('/')) return `Add a path, e.g. ${host}/*.`
  if (host.includes(':')) return 'Leave out the port.'
  const zoneHost = host.replace(/^[*.]+/, '')
  if (!zoneHost || !zoneHost.includes('.') || zoneHost.includes('*'))
    return 'Start with a domain name of a zone, optionally with a leading wildcard, e.g. *.example.com/*.'
}

/** The problem with a cron trigger, or undefined. */
export function cronProblem(input: string): string | undefined {
  const fields = input.trim().split(/\s+/).filter(Boolean)
  if (fields.length !== 5)
    return 'A cron trigger has five fields, e.g. */15 * * * *.'
  const bad = fields.find((f) => !CRON_FIELD.test(f))
  if (bad) return `"${bad}" is not a valid cron field.`
}

/** The problem with a secret or variable name, or undefined. */
export function bindingNameProblem(input: string): string | undefined {
  const n = input.trim()
  if (!n) return 'Enter a name.'
  if (n.length > 255 || !BINDING.test(n))
    return 'Use letters, digits and underscores, and do not start with a digit.'
}

/** The problem with a secret's value, or undefined. */
export function secretValueProblem(value: string): string | undefined {
  if (!value) return 'Enter the value.'
  if (new TextEncoder().encode(value).length > MAX_SECRET_BYTES)
    return 'A value is at most 5 KB.'
}

/** The problem with a new Pages project's name, or undefined. */
export function projectNameProblem(input: string): string | undefined {
  const n = input.trim()
  if (!n) return 'Enter a name.'
  if (n.length > 58 || !PROJECT.test(n))
    return 'Use at most 58 lowercase letters, digits and hyphens, starting with a letter or digit.'
}

/** The problem with a production branch name, or undefined. */
export function branchProblem(input: string): string | undefined {
  const b = input.trim()
  if (!b) return 'Enter the branch.'
  if (b.length > 255 || !BRANCH.test(b)) return 'That is not a Git branch name.'
}

/** What a typed-name confirmation needs to match. */
export function nameConfirmed(typed: string, name: string): boolean {
  return typed.trim() === name
}

/** The public address a Worker answers on at workers.dev. */
export function workersDevUrl(name: string, subdomain: string) {
  return subdomain ? `https://${name}.${subdomain}.workers.dev` : undefined
}

/** How much of the traffic each version of the live deployment gets. */
export function servedVersions(
  deployments: CloudflareWorkerDeployment[]
): Map<string, number> {
  // Cloudflare lists the newest deployment first.
  const live = deployments[0]
  return new Map((live?.versions ?? []).map((v) => [v.versionId, v.percentage]))
}

/**
 * Whether a version can be rolled back to: any that is not already taking
 * all the traffic.
 */
export function canRollbackTo(
  versionId: string,
  served: Map<string, number>
): boolean {
  return served.get(versionId) !== 100
}

/** How a Pages deployment stands, with the classes that colour its badge. */
export function deploymentStatus(d: CloudflarePagesDeployment): {
  label: string
  className: string
} {
  if (d.isSkipped)
    return { label: 'Skipped', className: 'bg-muted text-muted-foreground' }
  if (d.stageStatus === 'success') {
    return d.stage === 'deploy'
      ? {
          label: 'Success',
          className: 'bg-success-muted text-success-foreground',
        }
      : {
          label: `Success · ${d.stage}`,
          className: 'bg-success-muted text-success-foreground',
        }
  }
  if (d.stageStatus === 'failure' || d.stageStatus === 'failed')
    return {
      label: `Failed · ${d.stage}`,
      className: 'bg-danger-muted text-danger-foreground',
    }
  if (d.stageStatus === 'canceled')
    return { label: 'Canceled', className: 'bg-muted text-muted-foreground' }
  return {
    label: d.stage ? `${d.stage} · ${d.stageStatus || 'pending'}` : 'Pending',
    className: 'bg-warning-muted text-warning-foreground',
  }
}

/** Whether a deployment finished and went live. */
export function deploymentSucceeded(d: CloudflarePagesDeployment): boolean {
  return d.stage === 'deploy' && d.stageStatus === 'success'
}

/** The environment's name as the page shows it. */
export function environmentLabel(e: Env): string {
  return e === Env.PREVIEW ? 'Preview' : 'Production'
}

/** The Pages project's address at pages.dev. */
export function pagesDevUrl(subdomain: string) {
  return subdomain ? `https://${subdomain}` : undefined
}

const actionLabels: Record<string, string> = {
  set_workers_dev: 'workers.dev',
  attach_domain: 'Attached domain',
  detach_domain: 'Detached domain',
  add_route: 'Added route',
  delete_route: 'Deleted route',
  add_schedule: 'Added cron trigger',
  delete_schedule: 'Deleted cron trigger',
  put_secret: 'Set secret',
  delete_secret: 'Deleted secret',
  rollback: 'Rolled back to version',
  delete: 'Deleted Worker',
  create_project: 'Created project',
  update_settings: 'Changed settings',
  delete_project: 'Deleted project',
  retry_deployment: 'Retried deployment',
  rollback_deployment: 'Rolled back to deployment',
  delete_deployment: 'Deleted deployment',
  add_domain: 'Added domain',
  delete_domain: 'Deleted domain',
  set_env_var: 'Set variable',
  delete_env_var: 'Deleted variable',
  enable_routing: 'Enabled Email Routing',
  create_rule: 'Added routing rule',
  update_rule: 'Changed routing rule',
  enable_rule: 'Enabled routing rule',
  disable_rule: 'Disabled routing rule',
  delete_rule: 'Deleted routing rule',
  set_catch_all: 'Changed catch-all',
  create_address: 'Added destination address',
  delete_address: 'Deleted destination address',
}

/** Words for a logged change's action; an unknown action is shown as is. */
export function actionLabel(op: Pick<CloudflareResourceOperation, 'action'>) {
  return actionLabels[op.action] ?? op.action.replace(/_/g, ' ')
}

/**
 * The part of a logged change that says what it did, e.g. "enabled" for a
 * workers.dev change, from what was confirmed or else what was asked for.
 */
export function actionDetail(
  op: Pick<CloudflareResourceOperation, 'action' | 'after' | 'requested'>
): string {
  if (op.action === 'set_workers_dev') {
    const state = (op.after ?? op.requested)?.enabled
    return state === undefined ? '' : state ? 'enabled' : 'disabled'
  }
  return ''
}

/** A logged state, flattened to short key: value text. */
export function stateSummary(state: JsonObject | undefined): string {
  if (!state) return ''
  return Object.entries(state)
    .map(([k, v]) => `${k.replace(/_/g, ' ')}: ${stateValue(v)}`)
    .join(' · ')
}

function stateValue(v: unknown): string {
  if (v === null || v === undefined || v === '') return '-'
  if (Array.isArray(v)) return v.length ? v.map(stateValue).join(', ') : 'none'
  if (typeof v === 'object')
    return Object.entries(v as JsonObject)
      .map(([k, x]) => `${k}=${stateValue(x)}`)
      .join(', ')
  return String(v)
}

/** How the DNS part of adding a Pages domain went, for a message. */
export function pagesRecordOutcome(
  r: CloudflareTunnelRecordResult | undefined
): {
  tone: 'success' | 'warning' | 'none'
  message: string
} {
  if (!r) return { tone: 'none', message: '' }
  switch (r.status) {
    case RecordStatus.CREATED:
      return {
        tone: 'success',
        message: 'A proxied CNAME to the project was created.',
      }
    case RecordStatus.EXISTS:
      return {
        tone: 'success',
        message: r.message || 'DNS already points at the project.',
      }
    case RecordStatus.KEPT:
    case RecordStatus.NO_ZONE:
    case RecordStatus.CONFLICT:
    case RecordStatus.FAILED:
      return { tone: 'warning', message: r.message || 'DNS was not changed.' }
  }
  return { tone: 'none', message: '' }
}

/** What to tell the user once a change request succeeded. */
export function changeOutcome(
  title: string,
  res: { changed: boolean; operationLogError: string }
): { kind: 'success' | 'info'; title: string; warning?: string } {
  const warning = res.operationLogError || undefined
  return res.changed
    ? { kind: 'success', title, warning }
    : { kind: 'info', title: 'Already as requested; nothing changed', warning }
}
