import type { ReactNode } from 'react'
import { Card, CardContent } from '@/components/ui/card'

/** A headline number: label, value, and an optional line of context. */
export function StatTile({
  label,
  value,
  detail,
}: {
  label: string
  value: ReactNode
  detail?: ReactNode
}) {
  return (
    <Card className='py-4'>
      <CardContent className='space-y-1 px-4'>
        <div className='text-sm text-muted-foreground'>{label}</div>
        <div className='text-2xl font-semibold tracking-tight'>{value}</div>
        {detail && (
          <div className='text-xs text-muted-foreground'>{detail}</div>
        )}
      </CardContent>
    </Card>
  )
}
