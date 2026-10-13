import type { ReactNode } from 'react'

/** A titled part of a management page, with an action beside the title. */
export function Section({
  title,
  description,
  action,
  children,
}: {
  title: string
  description?: ReactNode
  action?: ReactNode
  children: ReactNode
}) {
  return (
    <section className='space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div>
          <h2 className='text-lg font-semibold'>{title}</h2>
          {description && (
            <p className='text-sm text-muted-foreground'>{description}</p>
          )}
        </div>
        {action}
      </div>
      {children}
    </section>
  )
}
