import { describe, expect, it } from 'vitest'
import { mcpConfig } from './SettingsPage'

describe('mcpConfig', () => {
  it('builds a Claude Code command', () => {
    expect(mcpConfig('pap_x.y', 'https://papers.example')).toBe('claude mcp add --transport http papers https://papers.example/mcp --header "Authorization: Bearer pap_x.y"')
  })
})
