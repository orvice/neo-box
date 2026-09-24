import { LayoutDashboard, Settings, UserRound, Users } from 'lucide-react'
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
