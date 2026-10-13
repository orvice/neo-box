import { createFileRoute } from '@tanstack/react-router'
import { CloudflareWorkerPage } from '@/features/cloudflare/worker-page'

export const Route = createFileRoute(
  '/_authenticated/connections/$connectionId/workers/$name'
)({
  component: CloudflareWorkerPage,
})
