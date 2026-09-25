import { Button } from '@/components/ui/button'
import { DialogFooter } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

/** The connection name field every provider form starts with. */
export function NameField({
  value,
  onChange,
  placeholder,
}: {
  value: string
  onChange: (value: string) => void
  placeholder?: string
}) {
  return (
    <div className='space-y-2'>
      <Label htmlFor='conn-name'>Name</Label>
      <Input
        id='conn-name'
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  )
}

/** Cancel + submit; put it inside the provider's <form>. */
export function FormFooter({
  pending,
  onCancel,
}: {
  pending: boolean
  onCancel: () => void
}) {
  return (
    <DialogFooter>
      <Button type='button' variant='outline' onClick={onCancel}>
        Cancel
      </Button>
      <Button type='submit' disabled={pending}>
        {pending ? 'Verifying…' : 'Save'}
      </Button>
    </DialogFooter>
  )
}
