import { Link } from '@tanstack/react-router'
import { useAuthStore, useIsAdmin } from '@/stores/auth-store'
import { useLayout } from '@/context/layout-provider'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from '@/components/ui/sidebar'
import { BrandMark } from '@/components/brand-mark'
import { accountNav, adminNav, generalNav } from './data/sidebar-data'
import { NavGroup } from './nav-group'
import { NavUser } from './nav-user'

export function AppSidebar() {
  const { collapsible, variant } = useLayout()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = useIsAdmin()

  const navUser = {
    name: user?.display_name || user?.displayName || user?.username || 'User',
    email: user?.username ? `@${user.username}` : '',
    avatar: user?.avatar_url || user?.avatarUrl || '',
  }

  return (
    <Sidebar collapsible={collapsible} variant={variant}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size='lg' asChild>
              <Link to='/'>
                <BrandMark size={32} />
                <div className='grid flex-1 text-start text-sm leading-tight'>
                  <span className='truncate font-semibold'>Neo Box</span>
                  <span className='truncate text-xs text-muted-foreground'>
                    Personal resources
                  </span>
                </div>
              </Link>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <NavGroup {...generalNav} />
        {isAdmin && <NavGroup {...adminNav} />}
        <NavGroup {...accountNav} />
      </SidebarContent>
      <SidebarFooter>
        <NavUser user={navUser} />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
