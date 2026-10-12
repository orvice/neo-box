import { WorkspaceOrgUnitSchema } from '@/gen/neobox/v1/google_workspace_pb'
import { create } from '@bufbuild/protobuf'
import { describe, expect, it } from 'vitest'
import { orgUnitOptions, orgUnitTreeRows } from './org-unit-tree'

describe('organization hierarchy', () => {
  it('excludes a unit and all descendants from its possible parents, without excluding similarly named siblings', () => {
    const paths = [
      '/Team',
      '/Team/Child',
      '/Team/Child/Grandchild',
      '/Teamwork',
      '/Other',
    ]
    expect(
      orgUnitOptions(
        paths.map((path) => ({ path })),
        '/Team'
      )
    ).toEqual(['/', '/Other', '/Teamwork'])
  })
  it('keeps the root once and orders parents before children with their full paths', () => {
    const units = ['/Team/Child', '/Other', '/Team', '/'].map((path, i) =>
      create(WorkspaceOrgUnitSchema, { id: String(i), path, name: path })
    )
    const rows = orgUnitTreeRows(units)
    expect(rows.map((o) => o.path)).toEqual([
      '/',
      '/Other',
      '/Team',
      '/Team/Child',
    ])
    expect(rows.map((o) => o.depth)).toEqual([0, 1, 1, 2])
    expect(rows[0].id).toBe('')
  })
  it('keeps descendants adjacent even when sibling names share a prefix', () => {
    const units = ['/Team', '/Team-one', '/Team/Child'].map((path) =>
      create(WorkspaceOrgUnitSchema, {
        id: path,
        path,
        name: path.split('/').pop()!,
      })
    )
    const rows = orgUnitTreeRows(units)
    const parent = rows.findIndex((o) => o.path === '/Team')
    expect(rows[parent + 1].path).toBe('/Team/Child')
  })
})
