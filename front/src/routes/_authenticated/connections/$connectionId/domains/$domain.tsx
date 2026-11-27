import { createFileRoute } from '@tanstack/react-router'
import { PorkbunDomainPage } from '@/features/porkbun/domain-page'

export const Route = createFileRoute(
  '/_authenticated/connections/$connectionId/domains/$domain'
)({
  component: PorkbunDomainPage,
})
