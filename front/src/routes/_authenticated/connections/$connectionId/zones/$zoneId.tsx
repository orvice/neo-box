import { createFileRoute } from '@tanstack/react-router'
import { CloudflareZonePage } from '@/features/cloudflare/zone-page'

export const Route = createFileRoute(
  '/_authenticated/connections/$connectionId/zones/$zoneId'
)({
  component: CloudflareZonePage,
})
