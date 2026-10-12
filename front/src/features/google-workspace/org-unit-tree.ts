import { WorkspaceOrgUnitSchema } from '@/gen/neobox/v1/google_workspace_pb'
import { create } from '@bufbuild/protobuf'
import type { WorkspaceOrgUnit } from '@/api/google-workspace'

export function orgUnitOptions(
  units: Pick<WorkspaceOrgUnit, 'path'>[],
  excluding?: string
) {
  return Array.from(new Set(['/', ...units.map((o) => o.path)]))
    .filter(
      (path) =>
        !excluding || (path !== excluding && !path.startsWith(excluding + '/'))
    )
    .sort((a, b) => a.localeCompare(b))
}

export function orgUnitTreeRows(units: WorkspaceOrgUnit[]) {
  const root = create(WorkspaceOrgUnitSchema, { name: 'Root', path: '/' })
  const rows: Array<WorkspaceOrgUnit & { depth: number }> = [
    { ...root, depth: 0 },
  ]
  const children = new Map<string, WorkspaceOrgUnit[]>()
  for (const unit of units) {
    if (unit.path === '/') continue
    const parent =
      unit.parentPath || unit.path.slice(0, unit.path.lastIndexOf('/')) || '/'
    children.set(parent, [...(children.get(parent) ?? []), unit])
  }
  const visited = new Set<string>(['/'])
  function walk(path: string) {
    for (const unit of (children.get(path) ?? [])
      .slice()
      .sort((a, b) => a.name.localeCompare(b.name))) {
      if (visited.has(unit.path)) continue
      visited.add(unit.path)
      rows.push({ ...unit, depth: unit.path.split('/').filter(Boolean).length })
      walk(unit.path)
    }
  }
  walk('/')
  // Restricted administrators may see a child without its parent.
  for (const unit of units)
    if (!visited.has(unit.path)) {
      visited.add(unit.path)
      rows.push({ ...unit, depth: unit.path.split('/').filter(Boolean).length })
      walk(unit.path)
    }
  return rows
}
