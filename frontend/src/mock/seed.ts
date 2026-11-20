import type { Actor, Block, BlockProps, BlockType, Paper, User, Workspace } from '../api/types'
import { keyBetween } from './order'

// Sample content for the in-browser demo. Everything here is made up.

export const ME: User = { id: 'u-me', email: 'you@example.com', name: 'You' }
export const ALICE: User = { id: 'u-alice', email: 'alice@example.com', name: 'Alice' }
export const BOB: User = { id: 'u-bob', email: 'bob@example.com', name: 'Bob' }

export const human = (u: User): Actor => ({ kind: 'human', user_id: u.id, name: u.name })
export const claudeFor = (u: User): Actor => ({ kind: 'agent', user_id: u.id, name: u.name, agent_name: 'Claude', token_id: 't-claude' })

type Spec = [BlockType, string, BlockProps?, Actor?] | ['table', string[][], BlockProps?, Actor?]

const t0 = Date.parse('2026-11-18T09:00:00Z')

function build(ws: string, paper: string, specs: Spec[], fallback: Actor): Block[] {
  const out: Block[] = []
  let order = ''
  specs.forEach((s, i) => {
    order = keyBetween(order, '')
    const [type, content, props = {}, actor = fallback] = s
    const id = `${paper}-b${i}`
    const at = new Date(t0 + i * 60000).toISOString()
    const base = { workspace_id: ws, paper_id: paper, version: 1, created_by: actor, created_at: at, updated_by: actor, updated_at: at }
    if (type === 'table') {
      const rows = content as string[][]
      const cols = Math.max(...rows.map((r) => r.length))
      out.push({ ...base, id, order, type, content: '', props: { rows: rows.length, cols, header_row: true, ...props } })
      rows.forEach((row, r) => row.forEach((cell, c) => {
        out.push({ ...base, id: `${id}-c${r}-${c}`, parent_id: id, order: 'V', type: 'table_cell', content: cell, props: { row: r, col: c } })
      }))
    } else {
      out.push({ ...base, id, order, type, content: content as string, props })
    }
  })
  return out
}

const MERMAID = `flowchart LR
  A[Leak detected] --> B{Key still active?}
  B -- yes --> C[Revoke now]
  B -- no --> D[Audit usage]
  C --> D
  D --> E[Write timeline]`

const STATS_HTML = `<div style="font-family:system-ui,sans-serif;display:grid;grid-template-columns:repeat(3,1fr);gap:10px">
  <div style="padding:14px;border-radius:8px;background:#eaf0fc"><div style="font-size:12px;color:#4a5566">Leaks this quarter</div><div style="font-size:28px;font-weight:600;color:#1a2230">3</div></div>
  <div style="padding:14px;border-radius:8px;background:#f3ecfb"><div style="font-size:12px;color:#4a5566">Median time to revoke</div><div style="font-size:28px;font-weight:600;color:#1a2230">11 min</div></div>
  <div style="padding:14px;border-radius:8px;background:#fdf3e3"><div style="font-size:12px;color:#4a5566">Keys found by scanners</div><div style="font-size:28px;font-weight:600;color:#1a2230">2 / 3</div></div>
</div>
<div id="bar" style="margin-top:14px;height:10px;border-radius:5px;background:#dde2e9;overflow:hidden"><div style="width:0;height:100%;background:#7b3fc4;transition:width 1.2s"></div></div>
<div style="font:12px system-ui;color:#4a5566;margin-top:4px">Rotation coverage 72% (this frame runs its own script)</div>
<script>setTimeout(()=>document.querySelector('#bar div').style.width='72%',300)</script>`

export function seed() {
  const users = [ME, ALICE, BOB]
  const owner = (u: User) => ({ subject: { kind: 'user' as const, id: u.id }, role: 'owner' as const })
  const editor = (u: User) => ({ subject: { kind: 'user' as const, id: u.id }, role: 'editor' as const })

  const workspaces: Omit<Workspace, 'role'>[] = [
    { id: 'ws-sec', name: 'Security Team', emoji: '🛡', bindings: [owner(ME), editor(ALICE), editor(BOB)] },
    { id: 'ws-prod', name: 'Product Notes', emoji: '📓', bindings: [owner(ME), editor(ALICE)] },
  ]

  const paper = (ws: string, id: string, title: string, by: Actor, specs: Spec[]) => {
    const blocks = build(ws, id, specs, by)
    const last = blocks.reduce((a, b) => (a.updated_at > b.updated_at ? a : b))
    const p: Paper = {
      id, workspace_id: ws, title, revision: 1, access: { restricted: false, bindings: [] },
      created_by: by, created_at: new Date(t0).toISOString(), updated_by: last.updated_by, updated_at: last.updated_at,
    }
    return { paper: p, blocks }
  }

  const papers = [
    paper('ws-sec', 'p-runbook', 'Leaked credential runbook', human(ALICE), [
      ['heading', 'Why this exists', { level: 2 }],
      ['paragraph', 'Our on-call runbook for **credential leaks**. Keep it short; link out for details.'],
      ['callout', 'Rotate first, investigate second.', { icon: '⚠️' }],
      ['numbered_list_item', 'Revoke the leaked key'],
      ['numbered_list_item', 'Search audit logs for any use since the leak'],
      ['numbered_list_item', 'Write the timeline below'],
      ['todo', 'Page the key owner', { checked: true }],
      ['todo', 'Notify affected teams'],
      ['table', [['Step', 'Owner', 'Target'], ['Revoke', 'On-call', '15 min'], ['Audit', 'Security', '4 h'], ['Postmortem', 'Owner team', '5 days']]],
      ['heading', 'Response flow', { level: 2 }, claudeFor(ALICE)],
      ['mermaid', MERMAID, {}, claudeFor(ALICE)],
      ['html', STATS_HTML, {}, claudeFor(ALICE)],
      ['heading', 'Timeline', { level: 2 }],
      ['paragraph', 'Nothing yet. Add entries as you go, newest last.'],
    ]),
    paper('ws-sec', 'p-threat', 'Threat model: payment API', claudeFor(BOB), [
      ['paragraph', 'Draft written by Claude from the design review notes. Humans: please correct anything wrong.'],
      ['heading', 'Assets', { level: 2 }],
      ['bulleted_list_item', 'Card tokens in the vault'],
      ['bulleted_list_item', 'Merchant API keys'],
      ['heading', 'Top risks', { level: 2 }],
      ['table', [['Risk', 'Likelihood', 'Mitigation'], ['Key reuse across merchants', 'Medium', 'Per-merchant scoped keys'], ['Replay of webhooks', 'Low', 'Signed timestamps']]],
      ['code', 'POST /v1/charges\nAuthorization: Bearer <merchant key>\nIdempotency-Key: <uuid>', { language: 'http' }],
    ]),
    paper('ws-prod', 'p-ideas', 'Ideas backlog', human(ME), [
      ['paragraph', 'Loose ideas. Claude groups and dedupes these every Friday.'],
      ['bulleted_list_item', 'Offline mode for the field app'],
      ['bulleted_list_item', 'Bulk export to CSV'],
      ['quote', 'If it takes more than two clicks, nobody will use it.'],
    ]),
  ]
  return { users, workspaces, papers }
}
