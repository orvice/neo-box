import { z } from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { ConnectionDetailPage } from '@/features/connections/connection-detail'

const searchSchema = z.object({
  // Google Workspace: the tenant to open, so a page can link back to it.
  tenant: z.string().optional(),
})

export const Route = createFileRoute(
  '/_authenticated/connections/$connectionId/'
)({
  component: ConnectionDetailPage,
  validateSearch: searchSchema,
})
