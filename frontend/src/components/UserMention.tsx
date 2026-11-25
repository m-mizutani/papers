import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router'
import { api } from '../api/client'
import type { Actor, PersonRef, UserSummary } from '../api/types'
import { tParts, useTranslation } from '../i18n'
import { Popover, type Anchor } from '../ui/Popover'
import { actorColor, initials } from '../utils/actors'
import { isEnter } from '../utils/keyboard'

// People, shown the same way everywhere: "@name" that opens a card with
// the papers the person created. A mention holds the user's ID and the
// name written with it; the name is never looked up again.

/** A user's name as a mention or a person value keeps it: one line, trimmed, at most 100 bytes. */
export function personName(name: string): string {
  let s = name.split(/\s+/).filter(Boolean).join(' ')
  const enc = new TextEncoder()
  while (enc.encode(s).length > 100) s = [...s].slice(0, -1).join('').trimEnd()
  return s
}

/** Where the papers list opens for one person's papers. */
export const papersByPath = (ws: string, user: string) => `/ws/${ws}/papers?place=all&creator=${encodeURIComponent(user)}`

function Face({ id, name, avatar, large }: { id: string; name: string; avatar?: string; large?: boolean }) {
  const cls = `face solo${large ? ' lg' : ''}`
  if (avatar) return <img className={cls} src={avatar} alt="" referrerPolicy="no-referrer" />
  return <span className={cls} style={{ background: actorColor({ kind: 'human', user_id: id, name }) }} aria-hidden>{initials(name)}</span>
}

/** One person as "@name". A click opens the person's card. */
export function UserMention({ id, name, ws, onRemove }: { id: string; name: string; ws?: string; onRemove?: () => void }) {
  const { t } = useTranslation()
  const params = useParams()
  const space = ws ?? params.ws ?? ''
  const [card, setCard] = useState<HTMLElement | null>(null)
  return (
    <>
      <span className="mention" role="button" tabIndex={0} data-user-id={id}
        onClick={(e) => { e.preventDefault(); e.stopPropagation(); setCard(card ? null : e.currentTarget) }}
        onKeyDown={(e) => { if (isEnter(e) || e.key === ' ') { e.preventDefault(); e.stopPropagation(); setCard(e.currentTarget) } }}>
        @{name}
        {onRemove && <button type="button" className="x" aria-label={t('unchoose')} onClick={(e) => { e.stopPropagation(); onRemove() }}>✕</button>}
      </span>
      {card && (
        <Popover anchor={card} onClose={() => setCard(null)} className="personcard" label={name}>
          {/* The card is a portal: React would pass its events on to what holds the mention (a row, a block, a picker). */}
          <div onClick={(e) => e.stopPropagation()} onMouseDown={(e) => e.stopPropagation()} onPointerDown={(e) => e.stopPropagation()}>
            <div className="pc-head"><Face id={id} name={name} large /><span className="pc-name">{name}</span></div>
            {space && <Link className="pc-link" to={papersByPath(space, id)} onClick={() => setCard(null)}>{t('papersByPerson')}</Link>}
          </div>
        </Popover>
      )}
    </>
  )
}

/** A writer: a person as a mention, an AI as "Claude (for @name)", papers itself as text. */
export function ActorMention({ actor }: { actor: Actor | undefined }) {
  const { t } = useTranslation()
  if (!actor) return null
  if (actor.kind === 'system') return <span>papers</span>
  const who = <UserMention id={actor.user_id} name={actor.name} />
  if (actor.kind === 'agent') return <span className="actor">{actor.agent_name || 'AI'} ({tParts('forUser', { name: who })})</span>
  if (actor.token_id) return <span className="actor">{who} ({t('viaApi')})</span>
  return who
}

/** People whose name or email contains q, asked 150 ms after the last change; older answers are dropped. */
export function useUserSearch(ws: string, q: string, enabled = true): { users: UserSummary[]; state: 'loading' | 'ok' | 'error' } {
  const [res, setRes] = useState<{ users: UserSummary[]; state: 'loading' | 'ok' | 'error' }>({ users: [], state: 'loading' })
  const seq = useRef(0)
  useEffect(() => {
    if (!enabled || !ws) return
    const n = ++seq.current
    setRes((r) => ({ users: r.users, state: 'loading' }))
    const timer = window.setTimeout(() => {
      api.users(ws, { q }).then(
        // A user without a name cannot be written as a mention or a person value.
        (users) => { if (n === seq.current) setRes({ users: users.filter((u) => personName(u.name) !== ''), state: 'ok' }) },
        () => { if (n === seq.current) setRes({ users: [], state: 'error' }) },
      )
    }, 150)
    return () => window.clearTimeout(timer)
  }, [ws, q, enabled])
  return res
}

/**
 * The names of users known only by ID (a filter in the URL), read once, 20
 * at a time; remember adds names already known (people just chosen).
 */
export function useUserNames(ws: string, ids: string[]): { names: Map<string, string>; remember: (people: PersonRef[]) => void } {
  const [names, setNames] = useState<Map<string, string>>(new Map())
  const asked = useRef(new Set<string>())
  const key = ids.join(',')
  useEffect(() => {
    const missing = key ? key.split(',').filter((id) => !asked.current.has(id)) : []
    if (!ws || missing.length === 0) return
    for (let i = 0; i < missing.length; i += 20) {
      const part = missing.slice(i, i + 20)
      part.forEach((id) => asked.current.add(id))
      api.users(ws, { ids: part }).then((users) => {
        setNames((m) => new Map([...m, ...users.map((u) => [u.id, u.name] as [string, string])]))
      }, () => part.forEach((id) => asked.current.delete(id)))
    }
  }, [ws, key])
  const remember = useCallback((people: PersonRef[]) => {
    setNames((m) => new Map([...m, ...people.map((p) => [p.user_id, p.name] as [string, string])]))
  }, [])
  return { names, remember }
}

/** The list of candidates, with its loading, empty and failed states. */
export function PeopleOptions({ users, state, active, onPick, onHover }: {
  users: UserSummary[]
  state: 'loading' | 'ok' | 'error'
  active: number
  onPick: (u: UserSummary) => void
  onHover: (i: number) => void
}) {
  const { t } = useTranslation()
  let body: ReactNode
  if (state === 'error') body = <div className="pp-empty">{t('peopleFailed')}</div>
  else if (users.length === 0) body = <div className="pp-empty">{state === 'loading' ? t('loadingPeople') : t('noPeople')}</div>
  else body = users.map((u, i) => (
    <button key={u.id} type="button" role="option" aria-selected={i === active} className={`mi${i === active ? ' hi' : ''}`}
      onMouseMove={() => onHover(i)} onClick={() => onPick(u)}>
      <Face id={u.id} name={u.name} avatar={u.avatar_url} />
      <span className="mt">{u.name}</span>
    </button>
  ))
  return (
    <div role="listbox" aria-label={t('people')} onMouseDown={(e) => e.preventDefault()}>
      <div className="mh">{t('people')}</div>
      {body}
    </div>
  )
}

/** The menu "@" opens in the editor. keyRef receives the keys the editor passes on while it is open. */
export function MentionMenu({ ws, query, anchor, onPick, onClose, keyRef }: {
  ws: string
  query: string
  anchor: Anchor
  onPick: (u: UserSummary) => void
  onClose: () => void
  keyRef: { current: ((key: string) => boolean) | null }
}) {
  const { t } = useTranslation()
  const { users, state } = useUserSearch(ws, query)
  const [active, setActive] = useState(0)
  useEffect(() => setActive(0), [query])
  keyRef.current = (key) => {
    if (key === 'Escape') { onClose(); return true }
    if ((key === 'ArrowDown' || key === 'ArrowUp') && users.length === 0) return false
    if (key === 'ArrowDown') { setActive((a) => Math.min(a + 1, Math.max(users.length - 1, 0))); return true }
    if (key === 'ArrowUp') { setActive((a) => Math.max(a - 1, 0)); return true }
    if (key === 'Enter') {
      const u = users[active]
      if (u) { onPick(u); return true }
      return false
    }
    return false
  }
  useEffect(() => () => { keyRef.current = null }, [keyRef])
  return (
    <Popover anchor={anchor} onClose={onClose} className="mentionpop" label={t('people')}>
      <PeopleOptions users={users} state={state} active={active} onPick={onPick} onHover={setActive} />
    </Popover>
  )
}

/** Chooses people: the chosen as mentions with ✕, a field that narrows the candidates. */
export function PeopleChooser({ ws, chosen, onChange, max = 100 }: {
  ws: string
  chosen: PersonRef[]
  onChange: (next: PersonRef[]) => void
  max?: number
}) {
  const { t } = useTranslation()
  const [q, setQ] = useState('')
  const [active, setActive] = useState(0)
  const { users, state } = useUserSearch(ws, q)
  const shown = users.filter((u) => !chosen.some((c) => c.user_id === u.id))
  const pick = (u: UserSummary) => {
    if (chosen.length >= max) return
    onChange([...chosen, { user_id: u.id, name: personName(u.name) }])
    setQ('')
    setActive(0)
  }
  return (
    <>
      <div className="tagin">
        {chosen.map((c) => <UserMention key={c.user_id} id={c.user_id} name={c.name} ws={ws} onRemove={() => onChange(chosen.filter((x) => x.user_id !== c.user_id))} />)}
        <input autoFocus value={q} aria-label={t('choosePeople')} placeholder={chosen.length ? '' : t('choosePeople')}
          onChange={(e) => { setQ(e.target.value); setActive(0) }}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') { e.preventDefault(); setActive((a) => Math.min(a + 1, Math.max(shown.length - 1, 0))) }
            else if (e.key === 'ArrowUp') { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)) }
            else if (e.key === 'Backspace' && q === '' && chosen.length) onChange(chosen.slice(0, -1))
            else if (isEnter(e)) {
              e.preventDefault()
              const u = shown[active]
              if (u) pick(u)
            }
          }} />
      </div>
      <PeopleOptions users={shown} state={state} active={active} onPick={pick} onHover={setActive} />
    </>
  )
}

/** The editor of a person value (a cell or the property panel). */
export function PersonPicker({ ws, value, anchor, label, onChange, onClose }: {
  ws: string
  value: PersonRef[] | undefined
  anchor: Anchor
  label: string
  onChange: (v: PersonRef[] | null) => void
  onClose: () => void
}) {
  const chosen = value ?? []
  return (
    <Popover anchor={anchor} onClose={onClose} className="dbpop" label={label}>
      <PeopleChooser ws={ws} chosen={chosen} onChange={(next) => onChange(next.length ? next : null)} />
    </Popover>
  )
}

/** A person value in a cell or the panel. */
export function PeopleView({ people }: { people: PersonRef[] }) {
  return <span className="chips">{people.map((p) => <UserMention key={p.user_id} id={p.user_id} name={p.name} />)}</span>
}
