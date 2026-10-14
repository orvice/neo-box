import { describe, expect, it } from 'vitest'
import {
  MAIL_CSP,
  attachmentBlocked,
  downloadName,
  formatBytes,
  htmlDocument,
  labelText,
  neutralizeHtml,
} from './gmail-mail'

const TAG = /<(script|iframe|frame|object|embed|applet|form|meta|base|link)\b/i

describe('neutralizeHtml', () => {
  it('removes scripts with their bodies and blocked tags', () => {
    const out = neutralizeHtml(
      '<p>Hi</p><script>alert(1)</script><SCRIPT src=x></SCRIPT><iframe src="http://x"></iframe><link rel=stylesheet href=x><form action=x><input></form>'
    )
    expect(out).toBe('<p>Hi</p>')
    expect(out).not.toMatch(TAG)
  })

  it('removes a meta refresh however it is written', () => {
    const variants = [
      '<meta http-equiv="refresh" content="0;url=http://x">',
      '<META HTTP-EQUIV=refresh CONTENT="0;url=http://x">',
      '<meta foo=">" http-equiv=refresh content="0;url=http://x">',
      '<meta/http-equiv=refresh content="0;url=http://x">',
      '<<meta http-equiv=refresh content="0;url=http://x">',
    ]
    for (const v of variants) {
      const out = neutralizeHtml(`<p>a</p>${v}<p>b</p>`)
      expect(out).not.toMatch(TAG)
      expect(out).not.toMatch(/<meta/i)
    }
  })

  it('escapes any blocked tag that survives removal', () => {
    const out = neutralizeHtml('<scr<script></script>ipt>alert(1)</script>')
    expect(out).not.toMatch(TAG)
    const broken = neutralizeHtml('<base href="x" <base href=y')
    expect(broken).not.toMatch(TAG)
  })

  it('keeps ordinary markup and inline styles', () => {
    const html =
      '<table><tr><td style="color:red"><a href="https://a.test">x</a></td></tr></table>'
    expect(neutralizeHtml(html)).toBe(html)
  })
})

describe('htmlDocument', () => {
  it('starts with a policy that allows no script, image or network request', () => {
    const doc = htmlDocument('<img src="http://tracker.test/p.gif"><p>x</p>')
    expect(doc).toContain(`content="${MAIL_CSP}"`)
    expect(MAIL_CSP).toContain("default-src 'none'")
    expect(MAIL_CSP).not.toMatch(/img-src|script-src|connect-src/)
    // The policy comes before the message's own markup.
    expect(doc.indexOf('Content-Security-Policy')).toBeLessThan(
      doc.indexOf('tracker.test')
    )
  })

  it('has exactly one meta element beyond the charset: its own policy', () => {
    const doc = htmlDocument(
      '<meta http-equiv=refresh content="0;url=x"><p>x</p>'
    )
    expect(doc.match(/<meta\b/gi)).toHaveLength(2)
  })
})

describe('downloadName', () => {
  it('keeps a plain name', () => {
    expect(downloadName('report 2025.pdf')).toBe('report 2025.pdf')
  })
  it('removes paths, control characters and leading dots', () => {
    expect(downloadName('../../etc/passwd')).toBe('_.._etc_passwd')
    expect(downloadName('a\u0000b\nc.txt')).toBe('a_b_c.txt')
    expect(downloadName('.bashrc')).toBe('bashrc')
    expect(downloadName('C:\\x\\y.exe')).toBe('C__x_y.exe')
  })
  it('falls back for an empty name and limits the length', () => {
    expect(downloadName('')).toBe('attachment')
    expect(downloadName('...')).toBe('attachment')
    expect([...downloadName('a'.repeat(500))]).toHaveLength(120)
  })
})

describe('formatBytes', () => {
  it('formats sizes', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1023)).toBe('1023 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(BigInt(5 * 1024 * 1024))).toBe('5.0 MB')
    expect(formatBytes(25 * 1024 * 1024)).toBe('25 MB')
    expect(formatBytes(-1)).toBe('—')
  })
})

describe('labelText', () => {
  const base = { name: 'Inbox', messagesTotal: 10, messagesUnread: 0 }
  it('leaves out counts that could not be read instead of showing zero', () => {
    expect(labelText({ ...base, countsKnown: false })).toBe('Inbox')
    expect(labelText({ ...base, countsKnown: true })).toBe('Inbox (10)')
    expect(labelText({ ...base, countsKnown: true, messagesUnread: 3 })).toBe(
      'Inbox (3 unread of 10)'
    )
  })
})

describe('attachmentBlocked', () => {
  it('says why a download is not offered', () => {
    expect(
      attachmentBlocked({ downloadable: true, tooLarge: false, inline: false })
    ).toBe('')
    expect(
      attachmentBlocked({ downloadable: false, tooLarge: false, inline: false })
    ).not.toBe('')
    expect(
      attachmentBlocked({ downloadable: true, tooLarge: true, inline: false })
    ).toContain('25 MB')
  })
})
