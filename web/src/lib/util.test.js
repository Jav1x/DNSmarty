import { describe, it, expect } from 'vitest'
import { normFqdn, parseCidr, shares, fmtBytes, ago, protoOf, pushHist, sparkPaths, aclShare, statsPath, clientStatsPath, cleanClientIp, pwStrength, deviceIcon, hwRows, fmtUptime, fmtMbps } from './util'

describe('normFqdn', () => {
  it('lowercases and trims trailing dot', () => expect(normFqdn(' Example.COM. ')).toBe('example.com'))
})
describe('parseCidr', () => {
  it('/32 single', () => expect(parseCidr('198.51.100.7/32').key).toBe('cidrSingle'))
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
  it('https:// → DoH', () => expect(protoOf('https://cloudflare-dns.com/dns-query')).toBe('doh'))
  it('host:853 → DoT', () => expect(protoOf('dns.quad9.net:853')).toBe('dot'))
  it('ip:53 → UDP', () => expect(protoOf('9.9.9.9:53')).toBe('udp'))
  it('tls:// → DoT', () => expect(protoOf('tls://dns.google')).toBe('dot'))
  it('https:// with port is still DoH', () => expect(protoOf('https://dns.google:443/dns-query')).toBe('doh'))
  it('case-insensitive scheme', () => expect(protoOf('HTTPS://dns.google/dns-query')).toBe('doh'))
})
describe('ago', () => {
  const tRu = (k, { n }) => ({ agoSec: `${n} с`, agoMin: `${n} мин`, agoHour: `${n} ч`, agoDay: `${n} д` })[k]
  it('seconds', () => expect(ago(new Date(Date.now()-12000).toISOString(), tRu)).toBe('12 с'))
  it('minutes', () => expect(ago(new Date(Date.now()-26*60000).toISOString(), tRu)).toBe('26 мин'))
})
describe('pushHist', () => {
  it('keeps the last n points', () => {
    let h = []
    for (let i = 1; i <= 5; i++) h = pushHist(h, i, 3)
    expect(h).toEqual([3, 4, 5])
  })
  it('grows from empty up to n', () => {
    let h = pushHist([], 7, 30)
    h = pushHist(h, 9, 30)
    expect(h).toEqual([7, 9])
  })
})
describe('sparkPaths', () => {
  it('line and closed area over the full range', () => {
    const { line, fill } = sparkPaths([0, 40], 200, 40)
    expect(line).toBe('M0,37 L200,3')
    expect(fill).toBe('M0,37 L200,3 L200,40 L0,40 Z')
  })
  it('flat series rides the middle', () => {
    const { line } = sparkPaths([10, 10, 10], 200, 40)
    expect(line).toBe('M0,20 L100,20 L200,20')
  })
  it('zeros stay on the middle, not a spike', () => {
    const { line } = sparkPaths([0, 0], 200, 40)
    expect(line).toBe('M0,20 L200,20')
  })
  it('too few points — no path', () => {
    expect(sparkPaths([5], 200, 40)).toBeNull()
  })
})
describe('aclShare', () => {
  it('share of refused among dns, in percent', () => {
    expect(aclShare([{ dns: 100, refused: 4 }, { dns: 100, refused: 0 }])).toBeCloseTo(2)
  })
  it('no dns — no share, no NaN', () => {
    expect(aclShare([])).toBe(0)
    expect(aclShare([{ dns: 0, refused: 5 }])).toBe(0)
  })
})

describe('statsPath', () => {
  it('window only', () => expect(statsPath('1h')).toBe('/api/stats?window=1h'))
  it('all window is the literal "all"', () => expect(statsPath('all')).toBe('/api/stats?window=all'))
})
describe('clientStatsPath', () => {
  it('ipv4 with window', () => expect(clientStatsPath('192.168.1.4', '6h')).toBe('/api/stats/client?ip=192.168.1.4&window=6h'))
  it('ipv6 colons are encoded', () => expect(clientStatsPath('2001:db8::1', '24h')).toBe('/api/stats/client?ip=2001%3Adb8%3A%3A1&window=24h'))
})
describe('cleanClientIp', () => {
  it('trims spaces', () => expect(cleanClientIp('  10.40.12.7 ')).toBe('10.40.12.7'))
  it('empty and null give empty string', () => {
    expect(cleanClientIp('   ')).toBe('')
    expect(cleanClientIp(null)).toBe('')
  })
})

describe('pwStrength', () => {
  it('pin: short lowercase is 0', () => expect(pwStrength('abc')).toBe(0))
  it('pin: xkcd-style password is at least 3', () => expect(pwStrength('Tr0ub4dour&3')).toBe(3))
  it('empty is 0', () => expect(pwStrength('')).toBe(0))
  it('12 lowercase letters — only the length point', () => expect(pwStrength('abcdefghijkl')).toBe(1))
  it('12 mixed-case letters with a digit', () => expect(pwStrength('Abcdefgh1234')).toBe(3))
  it('17 chars with symbol reaches the cap', () => expect(pwStrength('Tr0ub4dour&3n1gma')).toBe(4))
})

describe('deviceIcon', () => {
  it('iPhone is a phone', () => expect(deviceIcon('Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)')).toBe('📱'))
  it('Android is a phone', () => expect(deviceIcon('Mozilla/5.0 (Linux; Android 14; Pixel 8)')).toBe('📱'))
  it('Mobile substring is a phone', () => expect(deviceIcon('Opera Mini Mobile')).toBe('📱'))
  it('desktop UA is a computer', () => expect(deviceIcon('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15')).toBe('💻'))
  it('empty UA defaults to computer', () => expect(deviceIcon('')).toBe('💻'))
  it('undefined UA defaults to computer', () => expect(deviceIcon(undefined)).toBe('💻'))
})

describe('fmtUptime', () => {
  it('dash for missing', () => expect(fmtUptime(null)).toBe('—'))
  it('seconds', () => expect(fmtUptime(9)).toBe('9s'))
  it('hours and minutes', () => expect(fmtUptime(2 * 3600 + 5 * 60)).toBe('2h 5m'))
  it('days and hours', () => expect(fmtUptime(2 * 86400 + 3 * 3600)).toBe('2d 3h'))
})
describe('fmtMbps', () => {
  it('dash for missing', () => expect(fmtMbps(null)).toBe('—'))
  it('one decimal', () => expect(fmtMbps(12.34)).toBe('12.3 Mbps'))
})
describe('hwRows', () => {
  it('empty report renders dashes', () => {
    expect(hwRows({}).map(([, v]) => v)).toEqual(['—', '—', '—', '—'])
  })
  it('full report formats cpu, memory, system, disk', () => {
    const rows = Object.fromEntries(hwRows({
      cpu_model: 'Xeon', cpu_cores: 4, mem_total_mb: 2048, mem_used_pct: 40.6,
      os: 'Debian', kernel: '6.1', disk_total_gb: 20, disk_used_gb: 5.25,
    }))
    expect(rows.hwCpu).toBe('Xeon · 4')
    expect(rows.hwMem).toBe('2.0 GB · 41%')
    expect(rows.hwOs).toBe('Debian · 6.1')
    expect(rows.hwDisk).toBe('5.3 / 20.0 GB')
  })
})
