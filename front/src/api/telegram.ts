import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { TelegramService } from '@/gen/neobox/v1/telegram_pb'
import { makeClient } from './transport'

export {
  type CreateTelegramLinkTokenResponse,
  type GetTelegramBotStatusResponse,
  type TelegramBinding,
} from '@/gen/neobox/v1/telegram_pb'

const client = makeClient(TelegramService)

const keys = {
  status: ['telegram', 'status'] as const,
}

export function useTelegramBotStatus(options?: { poll?: boolean }) {
  return useQuery({
    queryKey: keys.status,
    queryFn: () => client.getTelegramBotStatus({}),
    refetchInterval: (query) =>
      options?.poll && !query.state.data?.binding ? 3_000 : false,
  })
}

export function useCreateTelegramLinkToken() {
  return useMutation({
    mutationFn: () => client.createTelegramLinkToken({}),
  })
}

export function useUnbindTelegram() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      await client.unbindTelegram({})
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.status }),
  })
}
