import { createFileRoute } from '@tanstack/react-router'
import { WorkspaceOverviewPage } from '@/features/google-workspace/overview-page'

export const Route = createFileRoute('/_authenticated/workspace')({
  component: WorkspaceOverviewPage,
})
