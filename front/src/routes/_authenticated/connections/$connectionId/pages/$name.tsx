import { createFileRoute } from '@tanstack/react-router'
import { CloudflarePagesProjectPage } from '@/features/cloudflare/pages-project-page'

export const Route = createFileRoute(
  '/_authenticated/connections/$connectionId/pages/$name'
)({
  component: CloudflarePagesProjectPage,
})
