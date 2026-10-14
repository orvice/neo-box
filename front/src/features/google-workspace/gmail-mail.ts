// Logic for reading Gmail mail (docs/adr/0010): showing untrusted HTML safely,
// naming downloads, and wording for sizes and labels. The backend does the
// access checks and the logging; nothing here is a security control on its own
// except the HTML handling, which is one of three layers (see htmlDocument).

export const MAIL_SCOPE = 'https://www.googleapis.com/auth/gmail.readonly'

// Neo Box's own limit on a downloaded attachment (backend: 25 MB).
export const MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024

export function formatBytes(size: number | bigint): string {
  const n = Number(size)
  if (!Number.isFinite(n) || n < 0) return '—'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 10 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

// Tags that could load something, run something, navigate, or change how the
// rest of the document is read. The sandbox and the CSP already stop them; this
// removes them as well so the message cannot even ask.
const BLOCKED_TAGS = [
  'script',
  'iframe',
  'frame',
  'frameset',
  'object',
  'embed',
  'applet',
  'form',
  'meta',
  'base',
  'link',
]

// neutralizeHtml removes blocked tags. Whole elements go first so a script's
// body does not end up as text; anything left (a tag broken up with odd
// quoting, say) has its opening `<` escaped, so no blocked tag can start.
export function neutralizeHtml(html: string): string {
  let out = html
  for (const tag of ['script', 'iframe', 'object', 'applet', 'form']) {
    out = out.replace(new RegExp(`<${tag}\\b[\\s\\S]*?</${tag}\\s*>`, 'gi'), '')
  }
  out = out.replace(
    new RegExp(`<(?:${BLOCKED_TAGS.join('|')})\\b[^>]*>`, 'gi'),
    ''
  )
  return out.replace(
    new RegExp(`<(?=(?:${BLOCKED_TAGS.join('|')})\\b)`, 'gi'),
    '&lt;'
  )
}

// A message's HTML is untrusted markup, and opening it must neither run code in
// the dashboard nor tell the sender it was opened. Three layers: this document
// carries a Content-Security-Policy that allows no script, image, font, frame
// or network request, only inline styles; blocked tags are removed first; and
// the caller shows it in an iframe with an empty `sandbox`, which also stops
// scripts, forms, popups and navigation of the page around it.
export const MAIL_CSP =
  "default-src 'none'; style-src 'unsafe-inline'; form-action 'none'; base-uri 'none'"

export function htmlDocument(html: string): string {
  return (
    '<!doctype html><html><head><meta charset="utf-8">' +
    `<meta http-equiv="Content-Security-Policy" content="${MAIL_CSP}">` +
    '<style>body{font-family:system-ui,sans-serif;font-size:14px;margin:12px;word-break:break-word}</style>' +
    '</head><body>' +
    neutralizeHtml(html) +
    '</body></html>'
  )
}

// A download's file name comes from the sender. Keep it to a plain name with
// no path, control characters or leading dots.
export function downloadName(name: string, fallback = 'attachment'): string {
  const cleaned = [...name]
    .map((c) =>
      c.charCodeAt(0) < 0x20 ||
      c.charCodeAt(0) === 0x7f ||
      /[\\/:*?"<>|]/.test(c)
        ? '_'
        : c
    )
    .join('')
    .replace(/^[.\s]+/, '')
    .trim()
  const limited = [...cleaned].slice(0, 120).join('')
  return limited || fallback
}

export type LabelLike = {
  name: string
  countsKnown: boolean
  messagesTotal: number
  messagesUnread: number
}

// Counts that could not be read are left out, never shown as zero.
export function labelText(l: LabelLike): string {
  if (!l.countsKnown) return l.name
  return l.messagesUnread > 0
    ? `${l.name} (${l.messagesUnread} unread of ${l.messagesTotal})`
    : `${l.name} (${l.messagesTotal})`
}

export type AttachmentLike = {
  downloadable: boolean
  tooLarge: boolean
  inline: boolean
}

// Why an attachment cannot be downloaded, or '' when it can.
export function attachmentBlocked(a: AttachmentLike): string {
  if (!a.downloadable) return 'Gmail did not provide this part for download'
  if (a.tooLarge) return 'Larger than 25 MB; take it from Gmail directly'
  return ''
}

export function recipients(value: string): string {
  return value.trim() || '—'
}
