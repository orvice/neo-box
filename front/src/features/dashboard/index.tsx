import { Boxes } from 'lucide-react'
import { useAuth } from '@/hooks/use-auth'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { EmptyState } from '@/components/empty-state'

export function Dashboard() {
  const { user } = useAuth()
  const name = user?.display_name || user?.displayName || user?.username || ''

  return (
    <Page>
      <PageHeader
        title={name ? `Welcome back, ${name}` : 'Welcome back'}
        subtitle='All of your third-party resources in one place.'
      />
      <PageScroll>
        <EmptyState
          icon={<Boxes className='h-6 w-6' />}
          title='No resources yet'
          description='Connected third-party accounts and the resources they hold will show up here.'
        />
      </PageScroll>
    </Page>
  )
}
