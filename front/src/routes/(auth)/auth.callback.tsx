import { createFileRoute } from '@tanstack/react-router'
import { LogtoCallback } from '@/features/auth/logto-callback'

export const Route = createFileRoute('/(auth)/auth/callback')({
  component: LogtoCallback,
})
