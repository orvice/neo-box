import { createFileRoute } from '@tanstack/react-router'
import { SettingsTelegram } from '@/features/settings/telegram'

export const Route = createFileRoute('/_authenticated/settings/telegram')({
  component: SettingsTelegram,
})
