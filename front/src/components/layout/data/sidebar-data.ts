import {
  Cable,
  LayoutDashboard,
  Settings,
  UserRound,
  Users,
} from 'lucide-react'
import { providerInfos } from '@/features/connections/provider-info'
import { type NavGroup } from '../types'

export const generalNav: NavGroup = {
  title: 'General',
  items: [
    {
      title: 'Dashboard',
      url: '/',
      icon: LayoutDashboard,
    },
  ],
}

export const resourcesNav: NavGroup = {
  title: 'Resources',
  items: [
    {
      title: 'Connections',
      icon: Cable,
      items: [
        { title: 'All connections', url: '/connections' },
        ...providerInfos.map((p) => ({
          title: p.label,
          url: '/connections',
          search: { provider: p.key },
          icon: p.icon,
        })),
      ],
    },
  ],
}

export const accountNav: NavGroup = {
  title: 'Account',
  items: [
    {
      title: 'Profile',
      url: '/profile',
      icon: UserRound,
    },
    {
      title: 'Settings',
      url: '/settings',
      icon: Settings,
    },
  ],
}

export const adminNav: NavGroup = {
  title: 'Admin',
  items: [
    {
      title: 'Users',
      url: '/users',
      icon: Users,
    },
  ],
}
