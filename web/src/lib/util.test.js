import { describe, it, expect } from 'vitest'
import { normFqdn, parseCidr, shares, fmtBytes, ago, protoOf } from './util'

describe('normFqdn', () => {
  it('lowercases and trims trailing dot', () => expect(normFqdn(' Example.COM. ')).toBe('example.com'))
})
describe('parseCidr', () => {
  it('/32 single', () => expect(parseCidr('198.51.100.7/32').msg).toMatch(/одиночный адрес/))
  it('/24 count', () => expect(parseCidr('10.0.0.0/24').addrs).toBe(256))
  it('bad octet', () => expect(parseCidr('999.0.0.0/24').ok).toBe(false))
  it('bad mask', () => expect(parseCidr('10.0.0.0/40').ok).toBe(false))
  it('ipv6 not supported in v1', () => expect(parseCidr('2001:db8::/48').ok).toBe(false))
})
describe('shares', () => {
  it('equal split', () => expect(shares([10,10,10])).toEqual([33,33,34]))
  it('zeros -> zeros, no NaN', () => expect(shares([0,0])).toEqual([0,0]))
  it('empty', () => expect(shares([])).toEqual([]))
})
describe('fmtBytes', () => {
  it('formats MB', () => expect(fmtBytes(1024*1024*5)).toMatch(/^5(\.0)? MB$/))
})
describe('protoOf', () => {
  // Закреплённые случаи из брифа (лаба 13).
  it('https:// → DoH', () => expect(protoOf('https://cloudflare-dns.com/dns-query')).toBe('doh'))
  it('host:853 → DoT', () => expect(protoOf('dns.quad9.net:853')).toBe('dot'))
  it('ip:53 → UDP', () => expect(protoOf('9.9.9.9:53')).toBe('udp'))
  // Края: tls:// тоже DoT; порт у https:// не меняет DoH; регистр схемы не важен.
  it('tls:// → DoT', () => expect(protoOf('tls://dns.google')).toBe('dot'))
  it('https:// with port is still DoH', () => expect(protoOf('https://dns.google:443/dns-query')).toBe('doh'))
  it('case-insensitive scheme', () => expect(protoOf('HTTPS://dns.google/dns-query')).toBe('doh'))
})
describe('ago', () => {
  it('seconds', () => expect(ago(new Date(Date.now()-12000).toISOString())).toBe('12 с'))
  it('minutes', () => expect(ago(new Date(Date.now()-26*60000).toISOString())).toBe('26 мин'))
})
