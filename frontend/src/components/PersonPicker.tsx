import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { useTranslation } from '../i18n'
import { Popover, type Anchor } from '../ui/Popover'
import { actorColor, initials } from '../utils/actors'
import { isEnter } from '../utils/keyboard'

/** A person to pick: a user ID and the name to show. */
export interface Person {
  id: string
  name: string
}

/** How long typing must pause before the candidates are asked for again. */
export const SEARCH_DELAY_MS = 200

/**
 * Picks the creator of a papers list: "me" and "all" always come first,
 * then the people search finds (without me). The value is me, all or a user ID.
 */
export function PersonPicker({ anchor, value, me, search, onPick, onClose }: {
  anchor: Anchor
  value: string
  me: Person
  search: (q: string) => Promise<Person[]>
  onPick: (value: string, person?: Person) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [q, setQ] = useState('')
  const [people, setPeople] = useState<Person[]>()
  const [hi, setHi] = useState(0)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => { input.current?.focus() }, [])
  // Answers for a query already left behind are dropped.
  useEffect(() => {
    let live = true
    const timer = window.setTimeout(() => {
      search(q.trim()).then((got) => { if (live) setPeople(got.filter((p) => p.id !== me.id)) }).catch(() => { if (live) setPeople([]) })
    }, q ? SEARCH_DELAY_MS : 0)
    return () => { live = false; window.clearTimeout(timer) }
  }, [q, search, me.id])

  const options: { value: string; person?: Person }[] = [{ value: 'me', person: me }, { value: 'all' }, ...(people ?? []).map((p) => ({ value: p.id, person: p }))]
  const pick = (i: number) => {
    const o = options[i]
    if (!o) return
    onClose()
    onPick(o.value, o.value === 'all' ? undefined : o.person)
  }
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setHi((i) => (i + 1) % options.length) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setHi((i) => (i - 1 + options.length) % options.length) }
    else if (isEnter(e)) { e.preventDefault(); pick(hi) }
  }
  const face = (p: Person) => <span className="face" style={{ background: actorColor({ kind: 'human', user_id: p.id, name: p.name }) }} aria-hidden>{initials(p.name)}</span>
  return (
    <Popover anchor={anchor} onClose={onClose} label={t('author')} className="ppick">
      <div onKeyDown={onKey}>
        <input ref={input} className="inp" value={q} placeholder={t('searchByName')} aria-label={t('searchByName')}
          onChange={(e) => { setQ(e.target.value); setHi(0) }} />
        <div role="listbox" aria-label={t('author')}>
          {options.map((o, i) => (
            <div key={o.value}>
              {i === 2 && <div className="msep" />}
              <button type="button" role="option" aria-selected={value === o.value} className={`mi${hi === i ? ' hi' : ''}`}
                onMouseEnter={() => setHi(i)} onClick={() => pick(i)}>
                {o.person ? face(o.person) : <span className="face all" aria-hidden>{t('everyone').slice(0, 1)}</span>}
                <span className="mt">{o.person ? `@${o.person.name}` : t('everyone')}</span>
                {o.value === 'me' && <span className="md">{t('youTag')}</span>}
                {value === o.value && <span className="ck" aria-hidden>✓</span>}
              </button>
            </div>
          ))}
          {people && people.length === 0 && <><div className="msep" /><div className="mnote">{t('noOneMatches')}</div></>}
        </div>
      </div>
    </Popover>
  )
}
