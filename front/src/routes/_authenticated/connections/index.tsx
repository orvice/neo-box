import { z } from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { ConnectionsPage } from '@/features/connections/connections-page'

const searchSchema = z.object({
  // Provider key, e.g. "nocodb"; unknown keys show every connection.
  provider: z.string().optional(),
})

export const Route = createFileRoute('/_authenticated/connections/')({
  component: ConnectionsPage,
  validateSearch: searchSchema,
})
