import { createFileRoute } from '@tanstack/react-router'
import { CloudflareTunnelPage } from '@/features/cloudflare/tunnel-page'

export const Route = createFileRoute(
  '/_authenticated/connections/$connectionId/tunnels/$tunnelId'
)({
  component: CloudflareTunnelPage,
})
