import { createFileRoute } from '@tanstack/react-router'
import { WasabiBucketPage } from '@/features/wasabi/bucket-detail'

export const Route = createFileRoute(
  '/_authenticated/connections/$connectionId/buckets/$bucket'
)({
  component: WasabiBucketPage,
})
