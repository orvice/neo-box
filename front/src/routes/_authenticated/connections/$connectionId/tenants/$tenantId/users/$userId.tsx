import { z } from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceUserPage } from '@/features/google-workspace/user-page'

const searchSchema = z.object({
  // Which tab is open, so a link can land on e.g. Gmail.
  tab: z
    .enum(['profile', 'aliases', 'groups', 'security', 'gmail', 'drive'])
    .optional(),
})

export const Route = createFileRoute(
  '/_authenticated/connections/$connectionId/tenants/$tenantId/users/$userId'
)({
  component: WorkspaceUserPage,
  validateSearch: searchSchema,
})
