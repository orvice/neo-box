import {
  CloudflarePagesDeploymentSchema,
  CloudflareResourceOperationSchema,
  CloudflareTunnelRecordResultSchema,
  CloudflareWorkerDeploymentSchema,
} from '@/gen/neobox/v1/cloudflare_pb'
import { create } from '@bufbuild/protobuf'
import { describe, expect, it } from 'vitest'
import { CloudflareTunnelRecordStatus as RecordStatus } from '@/api/cloudflare'
import {
  actionDetail,
  actionLabel,
  bindingNameProblem,
  branchProblem,
  canRollbackTo,
  changeOutcome,
  cronProblem,
  deploymentStatus,
  deploymentSucceeded,
  domainProblem,
  nameConfirmed,
  pagesRecordOutcome,
  projectNameProblem,
  routeProblem,
  secretValueProblem,
  servedVersions,
  stateSummary,
  workersDevUrl,
} from './manage'

describe('input checks', () => {
  it('checks custom domains', () => {
    expect(domainProblem('app.example.com')).toBeUndefined()
    expect(domainProblem(' App.Example.com ')).toBeUndefined()
    expect(domainProblem('')).toBeTruthy()
    expect(domainProblem('*.example.com')).toMatch(/wildcard/)
    expect(domainProblem('localhost')).toBeTruthy()
    expect(domainProblem('https://app.example.com')).toBeTruthy()
  })

  it('checks route patterns', () => {
    expect(routeProblem('example.com/*')).toBeUndefined()
    expect(routeProblem('*.example.com/api/*')).toBeUndefined()
    expect(routeProblem('')).toBeTruthy()
    expect(routeProblem('example.com')).toMatch(/path/)
    expect(routeProblem('https://example.com/*')).toMatch(/scheme/)
    expect(routeProblem('example.com:8080/*')).toMatch(/port/)
    expect(routeProblem('a b.com/*')).toBeTruthy()
    expect(routeProblem('*/*')).toBeTruthy()
    expect(routeProblem('com/*')).toBeTruthy()
  })

  it('checks cron triggers', () => {
    expect(cronProblem('*/15 * * * *')).toBeUndefined()
    expect(cronProblem('0 9 * * MON-FRI')).toBeUndefined()
    expect(cronProblem('* * * *')).toMatch(/five/)
    expect(cronProblem('* * * * * *')).toMatch(/five/)
    expect(cronProblem('* * * * $')).toMatch(/\$/)
  })

  it('checks binding names and secret values', () => {
    expect(bindingNameProblem('API_KEY')).toBeUndefined()
    expect(bindingNameProblem('')).toBeTruthy()
    expect(bindingNameProblem('1KEY')).toBeTruthy()
    expect(bindingNameProblem('my-key')).toBeTruthy()
    expect(secretValueProblem('x')).toBeUndefined()
    expect(secretValueProblem('')).toBeTruthy()
    expect(secretValueProblem('x'.repeat(5 * 1024))).toBeUndefined()
    expect(secretValueProblem('x'.repeat(5 * 1024 + 1))).toBeTruthy()
    // The limit is in bytes, not characters.
    expect(secretValueProblem('é'.repeat(3000))).toBeTruthy()
  })

  it('checks project names and branches', () => {
    expect(projectNameProblem('my-site')).toBeUndefined()
    expect(projectNameProblem('My-Site')).toBeTruthy()
    expect(projectNameProblem('-site')).toBeTruthy()
    expect(projectNameProblem('a'.repeat(59))).toBeTruthy()
    expect(branchProblem('main')).toBeUndefined()
    expect(branchProblem('release/1.0')).toBeUndefined()
    expect(branchProblem('')).toBeTruthy()
    expect(branchProblem('has space')).toBeTruthy()
  })

  it('matches a typed name exactly, ignoring outer spaces', () => {
    expect(nameConfirmed(' my-worker ', 'my-worker')).toBe(true)
    expect(nameConfirmed('My-Worker', 'my-worker')).toBe(false)
    expect(nameConfirmed('', 'my-worker')).toBe(false)
  })
})

describe('Workers', () => {
  it('builds the workers.dev address', () => {
    expect(workersDevUrl('api', 'me')).toBe('https://api.me.workers.dev')
    expect(workersDevUrl('api', '')).toBeUndefined()
  })

  it('reads the serving versions from the newest deployment', () => {
    const served = servedVersions([
      create(CloudflareWorkerDeploymentSchema, {
        id: 'd2',
        versions: [
          { versionId: 'v2', percentage: 90 },
          { versionId: 'v1', percentage: 10 },
        ],
      }),
      create(CloudflareWorkerDeploymentSchema, {
        id: 'd1',
        versions: [{ versionId: 'v1', percentage: 100 }],
      }),
    ])
    expect(served.get('v2')).toBe(90)
    expect(served.get('v1')).toBe(10)
    expect(canRollbackTo('v1', served)).toBe(true)
    expect(canRollbackTo('v3', served)).toBe(true)
    expect(canRollbackTo('v2', new Map([['v2', 100]]))).toBe(false)
    expect(servedVersions([]).size).toBe(0)
  })
})

describe('Pages deployments', () => {
  const d = (over: object) =>
    create(CloudflarePagesDeploymentSchema, { id: 'x', ...over })

  it('reads the status', () => {
    expect(
      deploymentStatus(d({ stage: 'deploy', stageStatus: 'success' })).label
    ).toBe('Success')
    expect(
      deploymentStatus(d({ stage: 'build', stageStatus: 'failure' })).label
    ).toBe('Failed · build')
    expect(
      deploymentStatus(d({ stage: 'build', stageStatus: 'active' })).label
    ).toBe('build · active')
    expect(deploymentStatus(d({ isSkipped: true })).label).toBe('Skipped')
    expect(deploymentStatus(d({ stageStatus: 'canceled' })).label).toBe(
      'Canceled'
    )
    expect(deploymentStatus(d({})).label).toBe('Pending')
  })

  it('knows which deployments went live', () => {
    expect(
      deploymentSucceeded(d({ stage: 'deploy', stageStatus: 'success' }))
    ).toBe(true)
    expect(
      deploymentSucceeded(d({ stage: 'build', stageStatus: 'success' }))
    ).toBe(false)
    expect(
      deploymentSucceeded(d({ stage: 'deploy', stageStatus: 'failure' }))
    ).toBe(false)
  })
})

describe('the operation log', () => {
  const op = (over: object) =>
    create(CloudflareResourceOperationSchema, { id: 'o', ...over })

  it('words actions, keeping unknown ones readable', () => {
    expect(actionLabel({ action: 'attach_domain' })).toBe('Attached domain')
    expect(actionLabel({ action: 'do_something_new' })).toBe('do something new')
  })

  it('says what a workers.dev change did', () => {
    expect(
      actionDetail(op({ action: 'set_workers_dev', after: { enabled: true } }))
    ).toBe('enabled')
    expect(
      actionDetail(
        op({ action: 'set_workers_dev', requested: { enabled: false } })
      )
    ).toBe('disabled')
    expect(actionDetail(op({ action: 'add_route' }))).toBe('')
  })

  it('flattens states', () => {
    expect(stateSummary(undefined)).toBe('')
    expect(stateSummary({ hostname: 'a.com', zone: 'a.com' })).toBe(
      'hostname: a.com · zone: a.com'
    )
    expect(stateSummary({ crons: [] })).toBe('crons: none')
    expect(stateSummary({ crons: ['* * * * *', '0 0 * * *'] })).toBe(
      'crons: * * * * *, 0 0 * * *'
    )
    expect(
      stateSummary({ versions: [{ version_id: 'v1', percentage: 100 }] })
    ).toBe('versions: version_id=v1, percentage=100')
    expect(stateSummary({ replaces: false, note: '' })).toBe(
      'replaces: false · note: -'
    )
  })
})

describe('outcomes', () => {
  it('reports a change, or that nothing needed changing', () => {
    expect(
      changeOutcome('Done', { changed: true, operationLogError: '' })
    ).toEqual({
      kind: 'success',
      title: 'Done',
      warning: undefined,
    })
    expect(
      changeOutcome('Done', { changed: false, operationLogError: '' }).kind
    ).toBe('info')
    expect(
      changeOutcome('Done', { changed: true, operationLogError: 'no log' })
        .warning
    ).toBe('no log')
  })

  it('tells how the CNAME for a Pages domain went', () => {
    const r = (status: RecordStatus, message = '') =>
      create(CloudflareTunnelRecordResultSchema, { status, message })
    expect(pagesRecordOutcome(undefined).tone).toBe('none')
    expect(pagesRecordOutcome(r(RecordStatus.CREATED)).message).toMatch(
      /project/
    )
    expect(pagesRecordOutcome(r(RecordStatus.EXISTS)).tone).toBe('success')
    const conflict = pagesRecordOutcome(
      r(RecordStatus.CONFLICT, 'A record exists')
    )
    expect(conflict).toEqual({ tone: 'warning', message: 'A record exists' })
  })
})
