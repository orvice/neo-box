import { createFileRoute } from '@tanstack/react-router'
import { NocoDBConnectionsPage } from '@/features/nocodb/connections'

export const Route = createFileRoute('/_authenticated/nocodb/')({
  component: NocoDBConnectionsPage,
})
