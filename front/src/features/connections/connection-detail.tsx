import { getRouteApi, Link } from '@tanstack/react-router'
import { ChevronLeft } from 'lucide-react'
import { useConnection } from '@/api/connections'
import { Skeleton } from '@/components/ui/skeleton'
import { Page, PageHeader } from '@/components/common/page-parts'
import { EmptyState } from '@/components/empty-state'
import { providerOf } from './providers'

const route = getRouteApi('/_authenticated/connections/$connectionId/')

/** Loads the connection and renders its provider's detail page. */
export function ConnectionDetailPage() {
  const { connectionId } = route.useParams()
  const { data, isLoading, error } = useConnection(connectionId)
  const def = data && providerOf(data.provider)

  if (data && def) return <def.Detail connection={data} />

  const back = (
    <Link to='/connections' className='inline-flex items-center gap-1'>
      <ChevronLeft className='h-3 w-3' /> Connections
    </Link>
  )
  return (
    <Page>
      <PageHeader title='Connection' breadcrumb={back} />
      {isLoading ? (
        <Skeleton className='h-40' />
      ) : (
        <EmptyState
          title={data ? 'Unsupported provider' : 'Connection not found'}
          description={
            error?.message ??
            (data ? 'This dashboard cannot show this connection yet.' : '')
          }
        />
      )}
    </Page>
  )
}
