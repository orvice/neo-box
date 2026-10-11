import { useEffect } from 'react'
import { CircleAlert, Copy, ShieldAlert } from 'lucide-react'
import {
  useCloudflareTunnelToken,
  type CloudflareTunnel,
} from '@/api/cloudflare'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { changeError } from './access'
import { copy } from './clipboard'
import { runCommands } from './tunnels'

/**
 * Shows how to run a Tunnel with cloudflared. The token is read when the
 * dialog opens and lives only in it: Neo Box does not store it, and closing
 * the dialog drops it.
 */
export function TunnelTokenDialog({
  connectionId,
  tunnel,
  onClose,
}: {
  connectionId: string
  tunnel: CloudflareTunnel
  onClose: () => void
}) {
  const {
    mutate,
    data: token,
    error,
    isPending,
  } = useCloudflareTunnelToken(connectionId)
  useEffect(() => {
    mutate(tunnel.id)
  }, [mutate, tunnel.id])

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>Connect cloudflared to {tunnel.name}</DialogTitle>
          <DialogDescription>
            Run one of these where the services are. The Tunnel shows as healthy
            once cloudflared connects.
          </DialogDescription>
        </DialogHeader>
        <Alert>
          <ShieldAlert />
          <AlertTitle>The token is a credential</AlertTitle>
          <AlertDescription>
            Anyone who has it can run this Tunnel. Neo Box does not keep it;
            open this dialog again to read it.
          </AlertDescription>
        </Alert>
        {isPending && <Skeleton className='h-32 w-full' />}
        {error && (
          <Alert variant='destructive'>
            <CircleAlert />
            <AlertTitle>The token could not be read</AlertTitle>
            <AlertDescription className='break-words'>
              {changeError(error).message}
            </AlertDescription>
          </Alert>
        )}
        {token && (
          <div className='min-w-0 space-y-3'>
            {runCommands(token).map((c) => (
              <div key={c.label} className='space-y-1'>
                <div className='flex items-center justify-between'>
                  <span className='text-sm font-medium'>{c.label}</span>
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => void copy(c.command)}
                    aria-label={`Copy: ${c.label}`}
                  >
                    <Copy className='h-3.5 w-3.5' /> Copy
                  </Button>
                </div>
                <pre className='overflow-x-auto rounded-md border bg-muted p-2 font-mono text-xs break-all whitespace-pre-wrap'>
                  {c.command}
                </pre>
              </div>
            ))}
          </div>
        )}
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
