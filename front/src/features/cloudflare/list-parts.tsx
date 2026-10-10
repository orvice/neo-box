import { useEffect, useState, type ReactNode } from 'react'
import {
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  KeyRound,
  RefreshCw,
  Search,
  ShieldAlert,
} from 'lucide-react'
import {
  useCloudflareFetching,
  useRefreshCloudflare,
  type CloudflarePageInfo,
} from '@/api/cloudflare'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { moduleProblem } from './access'
import { SETUP_GUIDE } from './format'

/** A search box that reports its value once typing pauses. */
export function SearchBox({
  value,
  onChange,
  placeholder,
}: {
  value: string
  onChange: (value: string) => void
  placeholder: string
}) {
  const [text, setText] = useState(value)
  useEffect(() => {
    if (text === value) return
    const t = setTimeout(() => onChange(text.trim()), 300)
    return () => clearTimeout(t)
  }, [text, value, onChange])
  return (
    <div className='relative w-full sm:w-72'>
      <Search className='absolute top-1/2 left-2.5 h-4 w-4 -translate-y-1/2 text-muted-foreground' />
      <Input
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder={placeholder}
        className='ps-8'
        aria-label={placeholder}
      />
    </div>
  )
}

/** Page X of Y with previous / next. */
export function Pager({
  info,
  onPage,
  busy,
}: {
  info: CloudflarePageInfo | undefined
  onPage: (page: number) => void
  busy?: boolean
}) {
  if (!info || info.totalPages <= 1) {
    return info && info.totalCount > 0 ? (
      <div className='text-xs text-muted-foreground'>
        {Number(info.totalCount).toLocaleString()} in all
      </div>
    ) : null
  }
  return (
    <div className='flex items-center justify-between gap-2 text-xs text-muted-foreground'>
      <span>
        Page {info.page} of {info.totalPages} ·{' '}
        {Number(info.totalCount).toLocaleString()} in all
      </span>
      <div className='flex gap-1'>
        <Button
          size='sm'
          variant='outline'
          disabled={busy || info.page <= 1}
          onClick={() => onPage(info.page - 1)}
          aria-label='Previous page'
        >
          <ChevronLeft className='h-4 w-4' />
        </Button>
        <Button
          size='sm'
          variant='outline'
          disabled={busy || info.page >= info.totalPages}
          onClick={() => onPage(info.page + 1)}
          aria-label='Next page'
        >
          <ChevronRight className='h-4 w-4' />
        </Button>
      </div>
    </div>
  )
}

/**
 * Why a list could not be read: a missing permission (other tabs still
 * work), a token Cloudflare rejects, or a failure.
 */
export function ProblemAlert({
  error,
  permission,
}: {
  error: unknown
  /** The token permission this list needs, e.g. "Workers Scripts · Read". */
  permission?: string
}) {
  const p = moduleProblem(error)
  const Icon =
    p.kind === 'permission'
      ? ShieldAlert
      : p.kind === 'token'
        ? KeyRound
        : CircleAlert
  return (
    <Alert variant={p.kind === 'error' ? 'destructive' : 'default'}>
      <Icon />
      <AlertTitle>{p.title}</AlertTitle>
      <AlertDescription>
        <p className='break-words'>{p.message}</p>
        {p.kind === 'permission' && (
          <p>
            {permission
              ? `Add "${permission}" to the token to see this. `
              : 'Add the permission to the token to see this. '}
            Nothing else is affected.{' '}
            <a
              href={SETUP_GUIDE}
              target='_blank'
              rel='noreferrer'
              className='underline underline-offset-2'
            >
              Token permissions
            </a>
          </p>
        )}
        {p.kind === 'token' && (
          <p>Edit the connection and paste a valid token.</p>
        )}
      </AlertDescription>
    </Alert>
  )
}

/** Re-reads everything shown for the connection from Cloudflare. */
export function RefreshButton({ connectionId }: { connectionId: string }) {
  const refresh = useRefreshCloudflare(connectionId)
  const fetching = useCloudflareFetching(connectionId)
  return (
    <Button
      variant='outline'
      onClick={() => void refresh()}
      disabled={fetching > 0}
    >
      <RefreshCw className={cn('h-4 w-4', fetching > 0 && 'animate-spin')} />
      Refresh
    </Button>
  )
}

/** Search, list and pager around one list. */
export function ListFrame({
  search,
  pager,
  notice,
  children,
}: {
  search: ReactNode
  pager: ReactNode
  notice?: ReactNode
  children: ReactNode
}) {
  return (
    <div className='space-y-4'>
      <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
        {search}
        {notice}
      </div>
      {children}
      {pager}
    </div>
  )
}
