import { useEffect, useState } from 'react'
import {
  Check,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Copy,
  KeyRound,
  Lock,
  RefreshCw,
  Search,
  ShieldAlert,
  Timer,
} from 'lucide-react'
import { toast } from 'sonner'
import {
  usePorkbunFetching,
  useRefreshPorkbun,
  type PorkbunPageInfo,
} from '@/api/porkbun'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { API_ACCESS_DOCS, porkbunProblem } from './format'

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

export function Pager({
  info,
  onPage,
  busy,
}: {
  info: PorkbunPageInfo | undefined
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

export function ProblemAlert({ error }: { error: unknown }) {
  const p = porkbunProblem(error)
  const Icon =
    p.kind === 'permission'
      ? ShieldAlert
      : p.kind === 'credentials'
        ? KeyRound
        : p.kind === 'api-access'
          ? Lock
          : p.kind === 'rate-limit'
            ? Timer
            : CircleAlert
  return (
    <Alert variant={p.kind === 'error' ? 'destructive' : 'default'}>
      <Icon />
      <AlertTitle>{p.title}</AlertTitle>
      <AlertDescription>
        <p className='break-words'>{p.message}</p>
        {p.kind === 'api-access' && (
          <p>
            Turn on <span className='font-medium'>API Access</span> for this
            domain in your{' '}
            <a
              href={API_ACCESS_DOCS}
              target='_blank'
              rel='noreferrer'
              className='underline underline-offset-2'
            >
              Porkbun dashboard
            </a>
            , then refresh.
          </p>
        )}
        {p.kind === 'credentials' && (
          <p>Edit the connection and paste a valid Porkbun API key pair.</p>
        )}
      </AlertDescription>
    </Alert>
  )
}

export function RefreshButton({ connectionId }: { connectionId: string }) {
  const refresh = useRefreshPorkbun(connectionId)
  const fetching = usePorkbunFetching(connectionId)
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

export function CopyValueButton({
  value,
  label = 'Copy value',
}: {
  value: string
  label?: string
}) {
  const [copied, setCopied] = useState(false)
  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      toast.error('Could not copy to clipboard')
    }
  }
  return (
    <Button
      type='button'
      size='icon'
      variant='ghost'
      className='h-6 w-6 shrink-0 text-muted-foreground hover:text-foreground'
      onClick={() => void handleCopy()}
      aria-label={label}
      title={label}
    >
      {copied ? (
        <Check className='h-3.5 w-3.5 text-emerald-600' />
      ) : (
        <Copy className='h-3.5 w-3.5' />
      )}
    </Button>
  )
}
