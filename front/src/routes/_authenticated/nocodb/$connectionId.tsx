import { createFileRoute } from '@tanstack/react-router'
import { ConnectionDetailPage } from '@/features/nocodb/connection-detail'

export const Route = createFileRoute('/_authenticated/nocodb/$connectionId')({
  component: ConnectionDetailPage,
})
