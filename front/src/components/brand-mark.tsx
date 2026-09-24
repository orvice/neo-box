import { Box } from 'lucide-react'
import { cn } from '@/lib/utils'

interface Props {
  className?: string
  size?: number
}

export function BrandMark({ className = '', size = 36 }: Props) {
  return (
    <span
      aria-hidden='true'
      className={cn(
        'inline-flex shrink-0 items-center justify-center rounded-lg border border-white/20 bg-[#2b658b] text-white',
        className
      )}
      style={{ height: size, width: size }}
    >
      <Box style={{ height: size * 0.6, width: size * 0.6 }} />
    </span>
  )
}
