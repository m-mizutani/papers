import type { Actor, Block, BlockProps, BlockType, Paper, TableLine, User, Workspace } from '../api/types'
import { keyBetween } from '../utils/order'

// Sample content for the in-browser demo. Everything here is made up.

export const ME: User = { id: 'MockUserYou00000000000', email: 'you@example.com', name: 'You' }
export const ALICE: User = { id: 'MockUserAlice000000000', email: 'alice@example.com', name: 'Alice' }
export const BOB: User = { id: 'MockUserBob00000000000', email: 'bob@example.com', name: 'Bob' }

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
    const base = { workspace_id: ws, paper_id: paper, rev: 1, created_by: actor, created_at: at, updated_by: actor, updated_at: at }
    if (type === 'table') {
      const grid = content as string[][]
      let k = ''
      const cols: TableLine[] = Array.from({ length: Math.max(...grid.map((r) => r.length)) }, (_, c) => ({ id: `${id}-col${c}`, order: (k = keyBetween(k, '')) }))
      k = ''
      const rows: TableLine[] = grid.map((_, r) => ({ id: `${id}-row${r}`, order: (k = keyBetween(k, '')) }))
      out.push({ ...base, id, order, type, content: '', props: { rows, cols, header_row: true, ...props } })
      grid.forEach((row, r) => row.forEach((cell, c) => {
        out.push({ ...base, id: `${id}-c${r}-${c}`, parent_id: id, order: 'V', type: 'table_cell', content: cell, props: { row_id: rows[r]?.id, col_id: cols[c]?.id } })
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
  const system: Actor = { kind: 'system', user_id: '', name: 'papers' }

  const workspaces: Workspace[] = [
    { id: 'security', name: 'Security Team', emoji: '🛡', description: 'Runbooks and incident notes. Write steps as numbered lists; commands go in code blocks.', group: 'Teams', home_paper_id: 'p-home-sec' },
    { id: 'product', name: 'Product Notes', emoji: '📓', description: 'Ideas and decisions about the product.', home_paper_id: 'p-home-prod' },
  ]

  const paper = (ws: string, id: string, title: string, by: Actor, specs: Spec[], extra: Partial<Paper> = {}) => {
    const blocks = build(ws, id, specs, by)
    const last = blocks.reduce((a, b) => (a.updated_at > b.updated_at ? a : b), blocks[0] as Block)
    const p: Paper = {
      id, workspace_id: ws, title, visibility: 'public', version_number: 0,
      created_by: by, created_at: new Date(t0).toISOString(), updated_by: last?.updated_by ?? by, updated_at: last?.updated_at ?? new Date(t0).toISOString(),
      ...extra,
    }
    return { paper: p, blocks }
  }

  const papers = [
    paper('security', 'p-home-sec', 'Security Team', system, [
      ['paragraph', 'Start here. Runbooks for on-call live below; ask Claude to summarise anything.'],
      ['paper', '', { paper_id: 'p-runbook' }],
      ['paper', '', { paper_id: 'p-threat' }],
    ]),
    paper('security', 'p-runbook', 'Leaked credential runbook', human(ALICE), [
      ['heading', 'Why this exists', { level: 2 }],
      ['paragraph', 'Our on-call runbook for **credential leaks**. [Rotate first]{.red}, investigate second.'],
      ['callout', 'Keep it short; link out for details.', { icon: '⚠️' }],
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
    ], { parent_id: 'p-home-sec' }),
    paper('security', 'p-threat', 'Threat model: payment API', claudeFor(BOB), [
      ['paragraph', 'Draft written by Claude from the design review notes. [Humans: please correct anything wrong.]{.bg-yellow}'],
      ['heading', 'Assets', { level: 2 }],
      ['bulleted_list_item', 'Card tokens in the vault'],
      ['bulleted_list_item', 'Merchant API keys'],
      ['heading', 'Top risks', { level: 2 }],
      ['table', [['Risk', 'Likelihood', 'Mitigation'], ['Key reuse across merchants', 'Medium', 'Per-merchant scoped keys'], ['Replay of webhooks', 'Low', 'Signed timestamps']]],
      ['code', 'POST /v1/charges\nAuthorization: Bearer <merchant key>\nIdempotency-Key: <uuid>', { language: 'http' }],
    ], { parent_id: 'p-home-sec' }),
    paper('security', 'p-standup', 'Weekly sync notes', human(ME), [
      ['paragraph', 'Notes from this week. Not filed yet.'],
      ['todo', 'Follow up on the scanner alerts'],
    ]),
    paper('security', 'p-alice-idea', 'Idea: secret scanning in CI', human(ALICE), [
      ['paragraph', 'Run the scanner on every pull request, not only nightly.'],
    ]),
    paper('product', 'p-home-prod', 'Product Notes', system, [
      ['paragraph', 'What we are building and why.'],
      ['paper', '', { paper_id: 'p-ideas' }],
    ]),
    paper('product', 'p-ideas', 'Ideas backlog', human(ME), [
      ['paragraph', 'Loose ideas. Claude groups and dedupes these every Friday.'],
      ['bulleted_list_item', 'Offline mode for the field app'],
      ['bulleted_list_item', 'Bulk export to CSV'],
      ['quote', 'If it takes more than two clicks, nobody will use it.'],
    ], { parent_id: 'p-home-prod' }),
    paper('product', 'p-memo', 'My scratchpad', human(ME), [
      ['paragraph', 'Only you can see this private paper.'],
    ], { visibility: 'private' }),
  ]
  return { users, workspaces, papers }
}
