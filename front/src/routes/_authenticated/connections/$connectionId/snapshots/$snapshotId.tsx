import { createFileRoute } from '@tanstack/react-router'
import { SnapshotDetailPage } from '@/features/nocodb/snapshot-detail'

export const Route = createFileRoute(
  '/_authenticated/connections/$connectionId/snapshots/$snapshotId'
)({
  component: SnapshotDetailPage,
})
