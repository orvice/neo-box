import { CloudflareAddressSchema } from '@/gen/neobox/v1/cloudflare_pb'
import { create } from '@bufbuild/protobuf'
import { describe, expect, it } from 'vitest'
import { CloudflareAddressKind as Kind } from '@/api/cloudflare'
import { addressLinks } from './addresses'

const addr = (kind: Kind, url: string, status = '') =>
  create(CloudflareAddressSchema, { kind, url, status })

describe('addressLinks', () => {
  it('opens URLs and leaves wildcard routes as patterns to copy', () => {
    const links = addressLinks([
      addr(Kind.WORKERS_DEV, 'https://api.acme.workers.dev'),
      addr(Kind.ROUTE, 'alpha.com/api/*'),
      addr(Kind.ROUTE, '*.alpha.com/hook'),
      addr(Kind.ROUTE, 'bravo.com/hook'),
    ])
    expect(links.map((l) => [l.text, l.href ?? null])).toEqual([
      ['https://api.acme.workers.dev', 'https://api.acme.workers.dev'],
      ['alpha.com/api/*', null],
      ['*.alpha.com/hook', null],
      ['bravo.com/hook', 'https://bravo.com/hook'],
    ])
    expect(links[1].pattern).toBe(true)
    expect(links[3].pattern).toBe(false)
  })

  it('lists each address once', () => {
    const links = addressLinks([
      addr(Kind.CUSTOM_DOMAIN, 'https://Alpha.com'),
      addr(Kind.ROUTE, 'alpha.com/'),
      addr(Kind.ROUTE, 'alpha.com/*'),
      addr(Kind.PAGES_DEV, 'https://blog.pages.dev/'),
      addr(Kind.PRODUCTION, 'https://blog.pages.dev'),
    ])
    expect(links.map((l) => l.text)).toEqual([
      'https://Alpha.com',
      'alpha.com/*',
      'https://blog.pages.dev/',
    ])
  })

  it('never links anything but http(s)', () => {
    const [link] = addressLinks([
      addr(Kind.CUSTOM_DOMAIN, 'javascript:alert(1)'),
    ])
    expect(link.href).toBeUndefined()
    expect(link.text).toBe('javascript:alert(1)')
  })

  it('links a custom domain only once it is active', () => {
    const links = addressLinks([
      addr(Kind.CUSTOM_DOMAIN, 'https://a.example.com', 'active'),
      addr(Kind.CUSTOM_DOMAIN, 'https://b.example.com', 'pending'),
      addr(Kind.CUSTOM_DOMAIN, 'https://c.example.com', 'unknown'),
      addr(Kind.CUSTOM_DOMAIN, 'https://d.example.com'),
    ])
    expect(links.map((l) => [l.note, l.href ?? null])).toEqual([
      [undefined, 'https://a.example.com'],
      ['pending', null],
      ['status unknown', null],
      [undefined, 'https://d.example.com'],
    ])
  })
})
