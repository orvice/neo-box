import { type LinkProps } from '@tanstack/react-router'

type BaseNavItem = {
  title: string
  badge?: string
  icon?: React.ElementType
}

/** Search params of a nav link, e.g. `{ provider: 'nocodb' }`. */
type NavSearch = Record<string, string>

type NavLink = BaseNavItem & {
  url: LinkProps['to'] | (string & {})
  search?: NavSearch
  items?: never
}

type NavSubItem = BaseNavItem & {
  url: LinkProps['to'] | (string & {})
  search?: NavSearch
}

type NavCollapsible = BaseNavItem & {
  items: NavSubItem[]
  url?: never
}

type NavItem = NavCollapsible | NavLink

type NavGroup = {
  title: string
  items: NavItem[]
}

/**
 * The `search` prop for a nav link. Nav URLs are plain strings, so the router
 * cannot type their search params (it resolves them to never); the cast is
 * confined here. An item without search params gets {}, which is also what a
 * Link without `search` navigates to.
 */
function navSearch(search: NavSearch | undefined) {
  return (() => search ?? {}) as never
}

export { navSearch }

export type {
  NavGroup,
  NavItem,
  NavCollapsible,
  NavLink,
  NavSearch,
  NavSubItem,
}
