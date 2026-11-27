import {
  keepPreviousData,
  useIsFetching,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import {
  PorkbunService,
  type PorkbunDomainFilter,
} from '@/gen/neobox/v1/porkbun_pb'
import { makeClient } from './transport'

export {
  PorkbunDomainFilter,
  type GetPorkbunDomainResponse,
  type PorkbunDNSRecord,
  type PorkbunDNSSECRecord,
  type PorkbunDomain,
  type PorkbunDomainLabel,
  type PorkbunGlueHost,
  type PorkbunPageInfo,
  type PorkbunPortfolioSummary,
  type PorkbunURLForward,
} from '@/gen/neobox/v1/porkbun_pb'

const client = makeClient(PorkbunService)

export type DomainListQuery = {
  query: string
  filter: PorkbunDomainFilter
  page: number
  pageSize: number
}

export type RecordListQuery = {
  query: string
  type: string
  page: number
  pageSize: number
}

const STALE_MS = 30_000

const keys = {
  all: (connectionId: string) => ['porkbun', connectionId] as const,
  domains: (connectionId: string, q: DomainListQuery) =>
    ['porkbun', connectionId, 'domains', q] as const,
  domain: (connectionId: string, domain: string) =>
    ['porkbun', connectionId, 'domain', domain] as const,
  records: (connectionId: string, domain: string, q: RecordListQuery) =>
    ['porkbun', connectionId, 'dns', domain, q] as const,
  forwards: (connectionId: string, domain: string) =>
    ['porkbun', connectionId, 'forwards', domain] as const,
  dnssec: (connectionId: string, domain: string) =>
    ['porkbun', connectionId, 'dnssec', domain] as const,
  glue: (connectionId: string, domain: string) =>
    ['porkbun', connectionId, 'glue', domain] as const,
}

export function usePorkbunDomains(connectionId: string, q: DomainListQuery) {
  return useQuery({
    queryKey: keys.domains(connectionId, q),
    queryFn: () =>
      client.listPorkbunDomains({
        connectionId,
        query: q.query,
        filter: q.filter,
        page: q.page,
        pageSize: q.pageSize,
      }),
    placeholderData: keepPreviousData,
    staleTime: STALE_MS,
  })
}

export function usePorkbunDomain(connectionId: string, domain: string) {
  return useQuery({
    queryKey: keys.domain(connectionId, domain),
    queryFn: () => client.getPorkbunDomain({ connectionId, domain }),
    enabled: Boolean(connectionId && domain),
    staleTime: STALE_MS,
  })
}

export function usePorkbunDNSRecords(
  connectionId: string,
  domain: string,
  q: RecordListQuery,
  enabled = true
) {
  return useQuery({
    queryKey: keys.records(connectionId, domain, q),
    queryFn: () =>
      client.listPorkbunDNSRecords({
        connectionId,
        domain,
        query: q.query,
        type: q.type,
        page: q.page,
        pageSize: q.pageSize,
      }),
    placeholderData: keepPreviousData,
    enabled: Boolean(connectionId && domain) && enabled,
    staleTime: STALE_MS,
  })
}

export function usePorkbunURLForwards(
  connectionId: string,
  domain: string,
  enabled = true
) {
  return useQuery({
    queryKey: keys.forwards(connectionId, domain),
    queryFn: async () =>
      (await client.listPorkbunURLForwards({ connectionId, domain })).forwards,
    enabled: Boolean(connectionId && domain) && enabled,
    staleTime: STALE_MS,
  })
}

export function usePorkbunDNSSECRecords(
  connectionId: string,
  domain: string,
  enabled = true
) {
  return useQuery({
    queryKey: keys.dnssec(connectionId, domain),
    queryFn: async () =>
      (await client.listPorkbunDNSSECRecords({ connectionId, domain })).records,
    enabled: Boolean(connectionId && domain) && enabled,
    staleTime: STALE_MS,
  })
}

export function usePorkbunGlueRecords(
  connectionId: string,
  domain: string,
  enabled = true
) {
  return useQuery({
    queryKey: keys.glue(connectionId, domain),
    queryFn: async () =>
      (await client.listPorkbunGlueRecords({ connectionId, domain })).hosts,
    enabled: Boolean(connectionId && domain) && enabled,
    staleTime: STALE_MS,
  })
}

export function useRefreshPorkbun(connectionId: string) {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: keys.all(connectionId) })
}

export function usePorkbunFetching(connectionId: string) {
  return useIsFetching({ queryKey: keys.all(connectionId) })
}
