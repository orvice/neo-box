import { Copy, ExternalLink } from 'lucide-react'
import { toast } from 'sonner'
import type { CloudflareAddress } from '@/api/cloudflare'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { addressKindLabel, addressLinks } from './addresses'

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.success('Copied')
  } catch {
    toast.error('Could not copy')
  }
}

/** A Worker's or Pages project's addresses, each openable and copyable. */
export function AddressList({
  addresses,
  incomplete,
}: {
  addresses: CloudflareAddress[]
  incomplete: boolean
}) {
  const links = addressLinks(addresses)
  return (
    <div className='space-y-1'>
      {links.length === 0 ? (
        <div className='text-xs text-muted-foreground'>No access address</div>
      ) : (
        links.map((l) => (
          <div key={l.key} className='flex min-w-0 items-center gap-1.5'>
            <Badge variant='secondary' className='shrink-0'>
              {addressKindLabel[l.kind]}
            </Badge>
            {l.href ? (
              <a
                href={l.href}
                target='_blank'
                rel='noreferrer noopener'
                className='truncate font-mono text-xs hover:underline'
                title={l.text}
              >
                {l.text}
              </a>
            ) : (
              <span
                className='truncate font-mono text-xs'
                title={l.pattern ? 'A route pattern, not a URL' : l.text}
              >
                {l.text}
              </span>
            )}
            {l.note && (
              <Badge variant='outline' className='shrink-0'>
                {l.note}
              </Badge>
            )}
            {l.href && (
              <Button asChild size='icon' variant='ghost' className='size-6'>
                <a
                  href={l.href}
                  target='_blank'
                  rel='noreferrer noopener'
                  aria-label={`Open ${l.text}`}
                >
                  <ExternalLink className='h-3 w-3' />
                </a>
              </Button>
            )}
            <Button
              size='icon'
              variant='ghost'
              className='size-6'
              onClick={() => void copy(l.text)}
              aria-label={`Copy ${l.text}`}
            >
              <Copy className='h-3 w-3' />
            </Button>
          </div>
        ))
      )}
      {incomplete && (
        <div className='text-xs text-warning-foreground'>
          Some address sources could not be read; there may be more.
        </div>
      )}
    </div>
  )
}
