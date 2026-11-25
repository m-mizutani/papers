import { describe, expect, it } from 'vitest'
import type { ApiToken } from '../api/types'
import type { MsgKey } from '../i18n'
import { formatIssued, mcpConfig, tokenSummary } from './SettingsPage'

describe('mcpConfig', () => {
  // T23, T29: the command names the MCP URL and carries no token
  it('builds a Claude Code command without a token', () => {
    expect(mcpConfig('https://papers-direct.example')).toBe('claude mcp add --transport http papers https://papers-direct.example/mcp')
    expect(mcpConfig('https://papers.example/')).toBe('claude mcp add --transport http papers https://papers.example/mcp')
    expect(mcpConfig('https://papers.example')).not.toContain('Authorization')
  })
})

// Intl may separate "PM" with a narrow no-break space.
const plain = (s: string) => s.replace(/[\u202f\u00a0]/g, ' ')

describe('formatIssued', () => {
  // T24
  it('shows the date and time in the time zone', () => {
    expect(plain(formatIssued('2026-11-25T09:00:00Z', 'ja', 'Asia/Tokyo'))).toBe('2026/11/25 18:00')
    expect(plain(formatIssued('2026-11-25T09:00:00Z', 'en', 'Asia/Tokyo'))).toBe('Nov 25, 2026, 6:00 PM')
  })
  // T25
  it('is empty when the time is missing or not a date', () => {
    expect(formatIssued(undefined, 'ja')).toBe('')
    expect(formatIssued('', 'ja')).toBe('')
    expect(formatIssued('not a date', 'en')).toBe('')
  })
})

describe('tokenSummary', () => {
  const t = (k: MsgKey, p?: Record<string, string | number>) => (p ? `${k}(${p.when})` : k)
  const base: ApiToken = { id: 't', name: 'Claude', kind: 'agent', scope: 'write', created_at: '2026-11-25T09:00:00Z' }

  // T24, T3: the row says it came through OAuth and when it was issued
  it('lists kind, access, OAuth, issue time and last use', () => {
    const s = plain(tokenSummary({ ...base, oauth: { client_name: 'Claude', redirect_uri: 'http://localhost:1/cb' } }, t, 'ja', 'Asia/Tokyo'))
    expect(s).toBe('tokenAgent · readWrite · tokenOAuth · issuedAt(2026/11/25 18:00) · neverUsed')
  })
  // T25
  it('leaves out the issue time when it is not a date', () => {
    expect(tokenSummary({ ...base, kind: 'personal', scope: 'read', created_at: 'x' }, t, 'en')).toBe('tokenPersonal · readOnly · neverUsed')
  })
})
