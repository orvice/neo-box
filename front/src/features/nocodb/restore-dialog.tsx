import { useState } from 'react'
import { Info } from 'lucide-react'
import { toast } from 'sonner'
import { Provider, useConnections } from '@/api/connections'
import { type Restore, type Snapshot, useRestoreSnapshot } from '@/api/nocodb'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  BASE_TITLE_MAX,
  defaultRestoreTitle,
  isValidBaseTitle,
  nocodbBaseUrl,
} from './format'

/** Restores a succeeded snapshot into a new Base on a chosen connection. */
export function RestoreDialog({
  snapshot,
  onClose,
  onQueued,
}: {
  snapshot: Snapshot
  onClose: () => void
  onQueued: (r: Restore) => void
}) {
  const connections = useConnections(Provider.NOCODB)
  const restore = useRestoreSnapshot()
  const [targetId, setTargetId] = useState(snapshot.connectionId)
  const [title, setTitle] = useState(() => defaultRestoreTitle(snapshot))
  const titleOk = isValidBaseTitle(title)

  function handleRestore() {
    restore.mutate(
      { snapshotId: snapshot.id, targetConnectionId: targetId, title },
      {
        onSuccess: (r) => {
          toast.success(`Restore of "${snapshot.baseTitle}" queued`)
          if (r) onQueued(r)
          onClose()
        },
        onError: (e) => toast.error(e.message),
      }
    )
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Restore “{snapshot.baseTitle || snapshot.baseId}”
          </DialogTitle>
          <DialogDescription>
            Rebuilds this snapshot into a new Base. Existing Bases are never
            changed.
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-5'>
          <div className='space-y-2'>
            <Label htmlFor='restore-target'>Restore into</Label>
            <Select value={targetId} onValueChange={setTargetId}>
              <SelectTrigger id='restore-target' className='w-full'>
                <SelectValue placeholder='Choose a connection' />
              </SelectTrigger>
              <SelectContent>
                {connections.data?.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    <span>{c.name}</span>
                    <span className='text-xs text-muted-foreground'>
                      {nocodbBaseUrl(c)}
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className='space-y-2'>
            <Label htmlFor='restore-title'>New Base title</Label>
            <Input
              id='restore-title'
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              aria-invalid={!titleOk}
            />
            <p
              className={
                titleOk
                  ? 'text-xs text-muted-foreground'
                  : 'text-xs text-danger-foreground'
              }
            >
              Letters, numbers, spaces, and - _ . ( ) &amp; , ' only; at most{' '}
              {BASE_TITLE_MAX} characters.
            </p>
          </div>
          <Alert>
            <Info />
            <AlertTitle>Not restored</AlertTitle>
            <AlertDescription>
              Attachments, views, filters, sorts, and webhooks; original record
              IDs; created and modified times and users; and User values for
              people who aren't members of the new Base. If a restore fails
              partway, the partial Base stays in NocoDB.
            </AlertDescription>
          </Alert>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={onClose}>
            Cancel
          </Button>
          <Button
            onClick={handleRestore}
            disabled={!titleOk || !targetId || restore.isPending}
          >
            Restore
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
