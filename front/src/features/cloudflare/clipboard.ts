import { toast } from 'sonner'

/** Copies text and says whether it worked. */
export async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.success('Copied')
  } catch {
    toast.error('Could not copy')
  }
}
