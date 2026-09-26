import type { ComponentType } from 'react'
import type { Connection, ConnectionSettings } from '@/api/connections'

export type ProviderFormProps = {
  /** The connection being edited; undefined when adding one. */
  connection?: Connection
  pending: boolean
  onSubmit: (name: string, settings: ConnectionSettings) => void
  onCancel: () => void
}

/** A provider's pages and forms. */
export type ProviderViews = {
  /** Shown above the connection form. */
  formHint: string
  /** What deleting a connection removes, for the confirm dialog. */
  deleteWarning: string
  /** Name and settings fields, plus the form footer. */
  Form: ComponentType<ProviderFormProps>
  /** A line or two about the connection on its card. */
  Summary: ComponentType<{ connection: Connection }>
  /** The connection's detail page. */
  Detail: ComponentType<{ connection: Connection }>
}
