import { createFileRoute } from '@tanstack/react-router'
import { ConnectionDetailPage } from '@/features/connections/connection-detail'

export const Route = createFileRoute(
  '/_authenticated/connections/$connectionId/'
)({
  component: ConnectionDetailPage,
})
