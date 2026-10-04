import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { NotificationService } from '@/gen/neobox/v1/notification_pb'
import { makeClient } from './transport'

export {
  AlertSeverity,
  type Alert,
  type NotificationChannel,
} from '@/gen/neobox/v1/notification_pb'

const client = makeClient(NotificationService)

const keys = {
  channels: ['notifications', 'channels'] as const,
  alerts: ['notifications', 'alerts'] as const,
}

/** A Telegram channel as entered; an empty token keeps the stored one. */
export interface TelegramInput {
  name: string
  chatId: string
  botToken: string
}

export function useNotificationChannels() {
  return useQuery({
    queryKey: keys.channels,
    queryFn: async () => (await client.listNotificationChannels({})).channels,
  })
}

export function useCreateNotificationChannel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: TelegramInput) =>
      (
        await client.createNotificationChannel({
          name: input.name,
          settings: {
            case: 'telegram',
            value: { chatId: input.chatId, botToken: input.botToken },
          },
        })
      ).channel,
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.channels }),
  })
}

export function useUpdateNotificationChannel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (
      input: TelegramInput & { id: string; enabled: boolean }
    ) =>
      (
        await client.updateNotificationChannel({
          id: input.id,
          name: input.name,
          enabled: input.enabled,
          settings: {
            case: 'telegram',
            value: { chatId: input.chatId, botToken: input.botToken },
          },
        })
      ).channel,
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.channels }),
  })
}

export function useDeleteNotificationChannel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      await client.deleteNotificationChannel({ id })
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.channels }),
  })
}

/** Sends a test message; resolves with the error text ('' when it worked). */
export function useTestNotificationChannel() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) =>
      (await client.testNotificationChannel({ id })).error,
    onSettled: () => qc.invalidateQueries({ queryKey: keys.channels }),
  })
}

export function useAlerts(limit = 50) {
  return useQuery({
    queryKey: [...keys.alerts, limit],
    queryFn: async () => (await client.listAlerts({ limit })).alerts,
    refetchInterval: 30_000,
  })
}
