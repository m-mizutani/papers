import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { ApiError, api } from '../api/client'
import { appendRows, applyDatabaseEvent, asPeople, clearValue, initDatabase, moveAfter, nextColor, placeItems, putValue, shownValue, visibleRows, type DatabaseState, type DbRow } from '../api/databaseState'
import { LiveConnection } from '../api/live'
import type { BatchResult, Element, FilterCondition, Me, Op, PaperEvent, PaperView, PersonRef, Property, Sidebar, Value, Workspace } from '../api/types'
import type { Layout } from '../App'
import { AddPropertyPopover, Chip, ColumnMenu, DateEditor, ElementPicker, TYPE_ICON, ValueView, type ElementActions } from '../components/PropertyEditors'
import { ago, tParts, useTranslation, type MsgKey } from '../i18n'
import { Menu, type MenuItem } from '../ui/Menu'
import { Modal } from '../ui/Modal'
import { Popover, type Anchor } from '../ui/Popover'
import { useToast } from '../ui/Toast'
import { actorName } from '../utils/actors'
import { ActorMention, PeopleChooser, PersonValuePicker, useUserNames } from '../components/UserMention'
import { MOD_LABEL, isEnter } from '../utils/keyboard'
import { paperIcon } from '../utils/icon'
import { IconPicker } from '../components/IconPicker'
import { NO_AUTOFILL } from '../utils/autofill'

// A database: its items as a table, one column per property. Definitions
// and the order of items are written through the database's batch, an
// item's title through the item's own. The sort and the filter live in the
// URL only (spec 2.9 D11).

const PAGE = 50
const phone = () => window.matchMedia('(max-width: 560px)').matches

type Cell = { item: string; prop: string; anchor: HTMLElement } | null

const OP_LABEL: Record<FilterCondition['op'], MsgKey> = {
  is: 'opIs', contains: 'opContains', on: 'opOn', before: 'opBefore', after: 'opAfter', between: 'opBetween',
  starts_with: 'opStartsWith', is_empty: 'opIsEmpty', is_not_empty: 'opIsNotEmpty',
}
const OPS_OF: Record<Property['type'], [FilterCondition['op'], ...FilterCondition['op'][]]> = {
  select: ['is', 'is_empty', 'is_not_empty'],
  multi_select: ['contains', 'is_empty', 'is_not_empty'],
  person: ['contains', 'is_empty', 'is_not_empty'],
  date: ['on', 'before', 'after', 'is_empty', 'is_not_empty'],
  text: ['starts_with', 'is_empty', 'is_not_empty'],
}

function parseFilter(raw: string | null): FilterCondition[] {
  if (!raw) return []
  try {
    const v: unknown = JSON.parse(raw)
    return Array.isArray(v) ? (v as FilterCondition[]) : []
  } catch {
    return []
  }
}

/** Whether a condition says enough to be sent. */
const complete = (c: FilterCondition) =>
  c.op === 'is_empty' || c.op === 'is_not_empty' || (c.elements?.length ?? 0) > 0 || (c.users?.length ?? 0) > 0 || !!c.date || !!c.text || (!!c.from && !!c.to)

export function DatabasePage({ ws, paperId, me, sidebar, onChanged, layout }: {
  ws: string
  paperId: string
  me: Me
  workspaces: Workspace[]
  sidebar?: Sidebar
  onChanged: () => void
  layout: Layout
}) {
  const { t } = useTranslation()
  const toast = useToast()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const sort = params.get('sort') ?? ''
  const filterRaw = params.get('filter')
  const filter = useMemo(() => parseFilter(filterRaw), [filterRaw])
  const sent = useMemo(() => filter.filter(complete), [filter])
  const query = useMemo(() => ({ sort: sort || undefined, filter: sent.length ? sent : undefined }), [sort, sent])

  const [view, setView] = useState<PaperView>()
  const [state, setState] = useState<DatabaseState>()
  const [loading, setLoading] = useState(true)
  const [next, setNext] = useState('')
  const [fatal, setFatal] = useState(false)
  const [sending, setSending] = useState(0)
  const [conn, setConn] = useState<'connecting' | 'open' | 'closed'>('connecting')
  const [title, setTitle] = useState('')
  const [cell, setCell] = useState<Cell>(null)
  const [textEdit, setTextEdit] = useState<{ item: string; prop: string; draft: string } | null>(null)
  const [titleEdit, setTitleEdit] = useState<{ item: string; draft: string } | null>(null)
  const [colMenu, setColMenu] = useState<{ prop: string; anchor: HTMLElement } | null>(null)
  const [adding, setAdding] = useState<Anchor | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<Property | null>(null)
  const [menu, setMenu] = useState<{ anchor: HTMLElement; items: MenuItem[]; label: string } | null>(null)
  const [iconPick, setIconPick] = useState<HTMLElement | null>(null)
  const iconRef = useRef<HTMLButtonElement>(null)
  const [cond, setCond] = useState<{ index: number; anchor: HTMLElement } | null>(null)
  const [focusItem, setFocusItem] = useState<string | null>(null)
  // A condition just added: its editor opens once its chip is drawn.
  const [condNext, setCondNext] = useState<number | null>(null)

  const stateRef = useRef<DatabaseState | undefined>(undefined)
  stateRef.current = state
  const queryRef = useRef(query)
  queryRef.current = query
  const loadSeq = useRef(0)
  const sentinel = useRef<HTMLTableRowElement>(null)
  const nextRef = useRef('')
  nextRef.current = next
  const reading = useRef(false)

  const put = useCallback((s: DatabaseState) => {
    stateRef.current = s
    setState(s)
  }, [])

  const load = useCallback(async () => {
    const seq = ++loadSeq.current
    setLoading(true)
    try {
      const v = await api.getPaper(ws, paperId)
      if (seq !== loadSeq.current) return
      setView(v)
      setTitle((cur) => (document.activeElement?.classList.contains('title-input') ? cur : v.paper.title))
      // The columns show before the items arrive (D3).
      put(initDatabase(v.paper))
      const page = await api.items(ws, paperId, { ...queryRef.current, limit: PAGE })
      if (seq !== loadSeq.current) return
      put(appendRows(initDatabase(stateRef.current?.db ?? v.paper), page.items))
      setNext(page.next_cursor)
    } catch (e) {
      if (seq !== loadSeq.current) return
      if (e instanceof ApiError && e.status === 404) setFatal(true)
      else toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
    } finally {
      if (seq === loadSeq.current) setLoading(false)
    }
  }, [ws, paperId, put, toast, t])

  const more = useCallback(async () => {
    const after = nextRef.current
    if (!after || reading.current) return
    reading.current = true
    const seq = loadSeq.current
    try {
      const page = await api.items(ws, paperId, { ...queryRef.current, after, limit: PAGE })
      const s = stateRef.current
      if (seq !== loadSeq.current || !s) return
      put(appendRows(s, page.items))
      setNext(page.next_cursor)
    } catch (e) {
      toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
    } finally {
      reading.current = false
    }
  }, [ws, paperId, put, toast, t])

  useEffect(() => {
    if (condNext === null) return
    const chip = document.querySelectorAll<HTMLElement>('.dbtools .cond')[condNext]
    if (!chip) return
    setCond({ index: condNext, anchor: chip })
    setCondNext(null)
  }, [condNext, filter])

  // The first read, and again when the sort or the filter changes.
  useEffect(() => { void load() }, [load, query])

  // Reads the next page when the end of the table comes near.
  useEffect(() => {
    const el = sentinel.current
    if (!el || !next) return
    const io = new IntersectionObserver((es) => { if (es.some((e) => e.isIntersecting)) void more() }, { rootMargin: '400px' })
    io.observe(el)
    return () => io.disconnect()
  }, [next, more, state?.order.length])

  // The database's change feed: definitions, items and property values.
  useEffect(() => {
    const live = new LiveConnection(api.liveURL(ws, paperId), {
      onHello: () => void load(),
      onStatus: setConn,
      onEvent: (ev: PaperEvent) => {
        const s = stateRef.current
        if (!s) return
        let n = applyDatabaseEvent(s, ev)
        const unsorted = !queryRef.current.sort && !queryRef.current.filter
        if (ev.type === 'item_put' && ev.paper && !s.rows.has(ev.paper.id) && unsorted && !ev.paper.deleted_at) {
          // An item someone else added shows at its place in the database's order.
          n = placeItems(s, [ev.paper], n.db.items ?? [])
        }
        if (ev.type === 'paper_updated' && unsorted && n.db !== s.db) n = placeItems(n, [], n.db.items ?? [])
        if (ev.type === 'paper_updated' && ev.paper?.id === paperId && n.db !== s.db) {
          if (ev.paper.title !== s.db.title || ev.paper.icon !== s.db.icon) onChanged()
          if (document.activeElement?.classList.contains('title-input') !== true) setTitle(ev.paper.title)
        }
        if (n !== s) put(n)
      },
      onClosing: () => setFatal(true),
    })
    return () => live.close()
  }, [ws, paperId, load, put, onChanged])

  // ---- writing ----

  /** Applies a batch's answer: the database record, new items, values. */
  const receive = useCallback((res: BatchResult, cleared: { item: string; prop: string }[] = []) => {
    let s = stateRef.current
    if (!s) return
    if (res.paper && res.paper.id === paperId) s = applyDatabaseEvent(s, { type: 'paper_updated', workspace_id: ws, paper_id: paperId, at: '', paper: res.paper })
    const unsorted = !queryRef.current.sort && !queryRef.current.filter
    // Without a sort the rows follow the database's order; with one, new
    // items are added at the end and the rest keep the sorted order.
    if (unsorted) s = placeItems(s, res.items ?? [], s.db.items ?? [])
    else if (res.items?.length) s = appendRows(s, res.items.map((item) => ({ item, values: [] })))
    for (const v of res.values ?? []) s = putValue(s, v)
    for (const c of cleared) s = clearValue(s, c.item, c.prop)
    put(s)
  }, [paperId, ws, put])

  const failed = useCallback((e: unknown, retry: () => void) => {
    if (e instanceof ApiError && e.status === 400) {
      const gone = /unknown element (\w+)/.exec(e.message)?.[1]
      const el = gone && stateRef.current?.db.properties?.flatMap((p) => p.elements ?? []).find((x) => x.id === gone)
      toast(el ? t('elementGone', { value: el.value }) : t('dbWriteFailed', { message: e.message }))
      void load()
    } else if (e instanceof ApiError && e.status === 403) {
      toast(t('error', { message: e.message }))
    } else {
      toast(t('sendFailed'), { label: t('retry'), run: retry })
    }
  }, [toast, t, load])

  /** Sends ops to the database's batch. */
  const send = useCallback(async (ops: Op[], cleared: { item: string; prop: string }[] = []): Promise<BatchResult | null> => {
    setSending((n) => n + 1)
    try {
      const res = await api.ops(ws, paperId, ops)
      receive(res, cleared)
      return res
    } catch (e) {
      failed(e, () => void send(ops, cleared))
      return null
    } finally {
      setSending((n) => n - 1)
    }
  }, [ws, paperId, receive, failed])

  /** Sets one property value of one item (null clears it). */
  const setValue = useCallback((item: string, prop: string, value: Value | null) => {
    const s = stateRef.current
    const row = s?.rows.get(item)
    const p = s?.db.properties?.find((x) => x.id === prop)
    if (!row || !p) return
    const cur = shownValue(p, row.values.get(prop))
    if (JSON.stringify(cur ?? null) === JSON.stringify(value)) return
    void send([{ op: 'set_values', item, set: { [prop]: value } }], value === null ? [{ item, prop }] : [])
  }, [send])

  /** Renames an item (the item's own batch). */
  const renameItem = useCallback(async (item: string, next: string) => {
    const row = stateRef.current?.rows.get(item)
    if (!row || row.item.title === next) return
    setSending((n) => n + 1)
    try {
      const res = await api.ops(ws, item, [{ op: 'set_title', title: next }])
      const s = stateRef.current
      if (s && res.paper) put(applyDatabaseEvent(s, { type: 'item_put', workspace_id: ws, paper_id: paperId, at: '', paper: res.paper }))
    } catch (e) {
      failed(e, () => void renameItem(item, next))
    } finally {
      setSending((n) => n - 1)
    }
  }, [ws, paperId, put, failed])

  const saveTitle = (next: string) => {
    const s = stateRef.current
    if (!s || s.db.title === next) return
    void send([{ op: 'set_title', title: next }]).then((r) => { if (r) onChanged() })
  }

  const newItem = async () => {
    const res = await send([{ op: 'insert_item', title: '' }])
    const id = res?.items?.[0]?.id
    if (!id) return
    if (phone()) navigate(`/ws/${ws}/p/${id}`)
    else {
      setTitleEdit({ item: id, draft: '' })
      setFocusItem(id)
    }
  }

  const elementActions = (row: DbRow, p: Property): ElementActions => ({
    create: (value) => {
      const multi = p.type === 'multi_select'
      const cur = shownValue(p, row.values.get(p.id))
      const chosen = multi ? [...((cur as string[] | undefined) ?? []), 'ref:new'] : 'ref:new'
      void send([
        { op: 'add_element', property: p.id, value, color: nextColor(p), ref: 'new' },
        { op: 'set_values', item: row.item.id, set: { [p.id]: chosen } },
      ])
    },
    update: (el, change) => void send([{ op: 'update_element', property: p.id, element: el.id, value: change.value, color: change.color }]),
    remove: (el) => void send([{ op: 'delete_element', property: p.id, element: el.id }]).then((r) => { if (r) toast(t('elementDeleted', { value: el.value })) }),
    move: (el, after) => void send([{ op: 'move_element', property: p.id, element: el.id, after: after ?? '' }]),
  })

  // ---- sort and filter (the URL) ----

  const setQuery = (nextSort: string, nextFilter: FilterCondition[]) => {
    const p = new URLSearchParams(params)
    if (nextSort) p.set('sort', nextSort)
    else p.delete('sort')
    if (nextFilter.length) p.set('filter', JSON.stringify(nextFilter))
    else p.delete('filter')
    setParams(p, { replace: true })
  }

  const props = state?.db.properties ?? []
  const propOf = (id: string) => props.find((p) => p.id === id)
  const filterUsers = useMemo(() => [...new Set(filter.flatMap((c) => c.users ?? []))], [filter])
  const { names: userNames, remember: rememberNames } = useUserNames(ws, filterUsers)

  const sortLabel = (): string => {
    const [key, dir] = sort.split(':')
    if (key === 'created') return t('sortCreatedNewest')
    if (key === 'updated') return t('sortUpdatedNewest')
    const name = key === 'title' ? t('colName') : propOf(key ?? '')?.name ?? '?'
    return `${name} ${dir === 'desc' ? '↓' : '↑'}`
  }

  const sortMenu = (anchor: HTMLElement) => {
    const items: MenuItem[] = [{ label: t('noSort'), icon: sort ? '' : '✓', onSelect: () => setQuery('', filter) }, '-']
    const add = (key: string, name: string) => {
      items.push({ label: `${name} · ${t('ascending')}`, icon: sort === `${key}:asc` ? '✓' : '', onSelect: () => setQuery(`${key}:asc`, filter) })
      items.push({ label: `${name} · ${t('descending')}`, icon: sort === `${key}:desc` ? '✓' : '', onSelect: () => setQuery(`${key}:desc`, filter) })
    }
    add('title', t('colName'))
    for (const p of props) if (p.type !== 'multi_select' && p.type !== 'person') add(p.id, p.name)
    items.push('-')
    items.push({ label: t('sortCreatedNewest'), icon: sort === 'created:desc' ? '✓' : '', onSelect: () => setQuery('created:desc', filter) })
    items.push({ label: t('sortUpdatedNewest'), icon: sort === 'updated:desc' ? '✓' : '', onSelect: () => setQuery('updated:desc', filter) })
    setMenu({ anchor, items, label: t('sortItems') })
  }

  const filterMenu = (anchor: HTMLElement) => {
    const items: MenuItem[] = props.map((p) => ({
      label: p.name, icon: TYPE_ICON[p.type], disabled: filter.length >= 5,
      onSelect: () => {
        const nf = [...filter, { property: p.id, op: OPS_OF[p.type][0] }]
        setQuery(sort, nf)
        setCondNext(nf.length - 1)
      },
    }))
    setMenu({ anchor, items: items.length ? items : [{ note: t('dbEmptyHint') }], label: t('filterItems') })
  }

  const condText = (c: FilterCondition): string => {
    const p = propOf(c.property)
    if (!p) return '?'
    if (c.op === 'is_empty' || c.op === 'is_not_empty') return `${p.name} ${t(OP_LABEL[c.op])}`
    if (c.users) return `${p.name}: ${c.users.map((id) => `@${userNames.get(id) ?? '…'}`).join(', ')}`
    if (c.elements) return `${p.name}: ${(p.elements ?? []).filter((e) => c.elements?.includes(e.id)).map((e) => e.value).join(', ')}`
    return `${p.name} ${t(OP_LABEL[c.op])} ${c.date ?? c.text ?? ''}`
  }

  // ---- rendering ----

  if (fatal && !state) {
    return (
      <section className="main">
        <header className="top"><button type="button" className="iconbtn" aria-label={t('toggleSidebar')} onClick={layout.toggleSidebar}>☰</button></header>
        <div className="scroll"><div className="col">
          <h1 className="page-h" style={{ paddingLeft: 'var(--gutter)' }}>{t('paperNotFound')}</h1>
          <p className="page-sub" style={{ paddingLeft: 'var(--gutter)' }}>{t('paperNotFoundBody')}</p>
        </div></div>
      </section>
    )
  }

  const db = state?.db
  const rows = state ? visibleRows(state) : []
  const canEdit = !!db && !db.deleted_at && !db.job_id
  const favorite = !!sidebar?.favorites.some((f) => f.id === paperId)
  const syncState = conn !== 'open' ? 'off' : sending > 0 ? 'busy' : ''
  const editingProp = colMenu ? propOf(colMenu.prop) : undefined
  const cellRow = cell ? state?.rows.get(cell.item) : undefined
  const cellProp = cell ? propOf(cell.prop) : undefined
  const names = props.map((p) => p.name)
  const condOpen = cond ? filter[cond.index] : undefined
  const condProp = condOpen ? propOf(condOpen.property) : undefined

  const openCell = (row: DbRow, p: Property, target: HTMLElement) => {
    if (!canEdit) return
    if (p.type === 'text') setTextEdit({ item: row.item.id, prop: p.id, draft: (shownValue(p, row.values.get(p.id)) as string | undefined) ?? '' })
    else setCell({ item: row.item.id, prop: p.id, anchor: target })
  }

  const toggleFavorite = async () => {
    try {
      if (favorite) await api.unfavorite(ws, paperId)
      else await api.favorite(ws, paperId)
      toast(favorite ? t('favoriteRemoved') : t('favoriteAdded'))
      onChanged()
    } catch (e) {
      toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
    }
  }

  const pageItems = (): MenuItem[] => {
    if (!db) return []
    const owner = db.created_by.user_id === me.user.id
    return [
      { label: t('copyLink'), icon: '🔗', onSelect: () => void navigator.clipboard?.writeText(`${window.location.origin}/ws/${ws}/p/${paperId}`).then(() => toast(t('copied')), () => undefined) },
      ...(canEdit ? [{ label: t('changeIcon'), icon: paperIcon(db), onSelect: () => setIconPick(iconRef.current ?? document.body) }] : []),
      { label: t('exportMarkdown'), icon: '⇩', onSelect: () => { window.location.href = api.markdownURL(ws, paperId) } },
      '-',
      db.archived_at
        ? { label: t('unarchive'), icon: '🗄', onSelect: () => void api.archive(ws, paperId, false, true).then(() => { toast(t('unarchivedDone')); onChanged(); void load() }, (e: unknown) => failed(e, () => undefined)) }
        : { label: t('archive'), icon: '🗄', onSelect: () => void api.archive(ws, paperId, true, true).then(() => { toast(t('archivedDone')); onChanged(); void load() }, (e: unknown) => failed(e, () => undefined)) },
      {
        label: t('moveToTrash'), icon: '🗑', danger: true, disabled: !owner, desc: owner ? undefined : t('trashNotOwner'),
        onSelect: () => void api.trashPaper(ws, paperId).then(() => {
          toast(t('trashed'))
          onChanged()
          navigate(db.parent_id ? `/ws/${ws}/p/${db.parent_id}` : `/ws/${ws}/papers`)
        }, (e: unknown) => failed(e, () => undefined)),
      },
      '-',
      { note: t('createdBy', { name: actorName(db.created_by) }) },
    ]
  }

  const colSpan = props.length + 2

  return (
    <>
      <section className="main">
        <header className="top">
          <button type="button" className="iconbtn" aria-label={t('toggleSidebar')} title={`${t('toggleSidebar')} (${MOD_LABEL}\\)`} onClick={layout.toggleSidebar}>☰</button>
          <nav className="crumbs" aria-label={t('location')}>
            {view && db?.parent_id && (view.path ?? []).map((p, i) => (
              <span key={p.id} style={{ display: 'contents' }}>{i > 0 && <span className="crumb-sep">/</span>}<Link className="crumb" to={i === 0 ? `/ws/${ws}` : `/ws/${ws}/p/${p.id}`}><span className="pic-i" aria-hidden>{paperIcon(p, i === 0 ? p.id : undefined)}</span> {p.title || t('untitled')}</Link></span>
            ))}
            {view && !db?.parent_id && <Link className="crumb" to={`/ws/${ws}/papers`}>{t('unfiled')}</Link>}
            {view && <><span className="crumb-sep">/</span><span className="crumb cur"><span className="pic-i" aria-hidden>{db ? paperIcon(db) : ''}</span> {title.trim() || t('untitledDatabase')}</span></>}
          </nav>
          {db && (
            <div className="top-r">
              <span className={`sync ${syncState}`} role="status">
                <span className="dot" />
                <span className="tx">{conn !== 'open' ? t('reconnecting') : sending > 0 ? t('syncing') : t('synced')}</span>
              </span>
              <button type="button" className={`iconbtn star${favorite ? ' on' : ''}`} aria-pressed={favorite} aria-label={favorite ? t('removeFavorite') : t('addFavorite')} title={favorite ? t('removeFavorite') : t('addFavorite')} onClick={() => void toggleFavorite()}>
                <svg width="17" height="17" viewBox="0 0 24 24" aria-hidden><path d="M12 3.5l2.6 5.3 5.9.9-4.25 4.1 1 5.8L12 16.9l-5.25 2.7 1-5.8L3.5 9.7l5.9-.9z" fill={favorite ? 'currentColor' : 'none'} stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" /></svg>
              </button>
              <button type="button" className="iconbtn" aria-label={t('more')} aria-haspopup="menu" onClick={(e) => setMenu({ anchor: e.currentTarget, items: pageItems(), label: t('more') })}>⋯</button>
            </div>
          )}
        </header>
        <div className="scroll">
          <div className="col wide">
            {db && (
              <>
                <div className="meta">
                  <span className="kindmark"><span aria-hidden>⊞</span>{t('database')}</span>
                  {db.archived_at && <><span className="mdot" /><span>{t('archived')}</span></>}
                  <span className="mdot" /><span>{tParts('editedBy', { name: <ActorMention actor={db.updated_by} />, when: ago(db.updated_at) })}</span>
                </div>
                {db.deleted_at && <div className="banner info"><span className="sp">{t('inTrash')}</span></div>}
                {db.archived_at && <div className="banner info"><span className="sp">{t('archivedBanner')}</span></div>}
                {db.icon
                  ? (canEdit
                    ? <button type="button" ref={iconRef} className="page-icon" aria-label={t('changeIcon')} title={t('changeIcon')} onClick={(e) => setIconPick(e.currentTarget)}>{db.icon}</button>
                    : <span className="page-icon" role="img" aria-label={t('iconLabel')}>{db.icon}</span>)
                  : canEdit && <button type="button" ref={iconRef} className="add-icon" onClick={(e) => setIconPick(e.currentTarget)}><span aria-hidden>☺</span>{t('addIcon')}</button>}
                <textarea {...NO_AUTOFILL} className="title-input" rows={1} value={title} placeholder={t('untitledDatabase')} aria-label={t('paperTitle')} readOnly={!canEdit}
                  onChange={(e) => setTitle(e.target.value.replace(/\n/g, ''))}
                  onBlur={(e) => saveTitle(e.target.value)}
                  onKeyDown={(e) => { if (isEnter(e)) { e.preventDefault(); e.currentTarget.blur() } }} />

                <div className="dbtools" role="toolbar" aria-label={t('database')}>
                  <button type="button" className="btn ghost sm" onClick={(e) => sortMenu(e.currentTarget)}>⇅ {t('sortItems')}</button>
                  <button type="button" className="btn ghost sm" disabled={props.length === 0} onClick={(e) => filterMenu(e.currentTarget)}>⏷ {t('filterItems')}</button>
                  {sort && (
                    <span className="chip c-blue">{sortLabel()}<button type="button" className="x" aria-label={t('noSort')} onClick={() => setQuery('', filter)}>✕</button></span>
                  )}
                  {filter.map((c, i) => (
                    <span key={i} className="chip c-gray cond" role="button" tabIndex={0}
                      onClick={(e) => setCond({ index: i, anchor: e.currentTarget })}
                      onKeyDown={(e) => { if (isEnter(e)) setCond({ index: i, anchor: e.currentTarget }) }}>
                      {condText(c)}
                      <button type="button" className="x" aria-label={t('removeCondition')} onClick={(e) => { e.stopPropagation(); setQuery(sort, filter.filter((_, j) => j !== i)) }}>✕</button>
                    </span>
                  ))}
                  <span className="sp" />
                  {sent.length > 0 && <span>{t('shownCount', { n: rows.length })}</span>}
                </div>

                <div className="dbwrap">
                  <table className="dbt">
                    <thead>
                      <tr>
                        <th className="tt"><button type="button" disabled><span className="ti" aria-hidden>Aa</span>{t('colName')}{sort.startsWith('title:') && (sort.endsWith('desc') ? ' ↓' : ' ↑')}</button></th>
                        {props.map((p) => (
                          <th key={p.id}>
                            <button type="button" className={colMenu?.prop === p.id ? 'on' : ''} disabled={!canEdit} aria-haspopup="dialog" onClick={(e) => setColMenu({ prop: p.id, anchor: e.currentTarget })}>
                              <span className="ti" aria-hidden>{TYPE_ICON[p.type]}</span>{p.name}{sort.startsWith(`${p.id}:`) && (sort.endsWith('desc') ? ' ↓' : ' ↑')}
                            </button>
                          </th>
                        ))}
                        <th className="add">{canEdit && <button type="button" aria-label={t('addProperty')} title={t('addProperty')} onClick={(e) => setAdding(e.currentTarget)}>＋</button>}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((row) => (
                        <tr key={row.item.id} data-item-id={row.item.id}>
                          <td className="tt">
                            <div className="tcell">
                              {titleEdit?.item === row.item.id ? (
                                <input {...NO_AUTOFILL} autoFocus={focusItem === row.item.id || undefined} value={titleEdit.draft} placeholder={t('untitled')} aria-label={t('colName')}
                                  onChange={(e) => setTitleEdit({ item: row.item.id, draft: e.target.value })}
                                  onBlur={() => { void renameItem(row.item.id, titleEdit.draft); setTitleEdit(null); setFocusItem(null) }}
                                  onKeyDown={(e) => {
                                    if (isEnter(e)) { e.preventDefault(); e.currentTarget.blur() }
                                    if (e.key === 'Escape') { setTitleEdit(null); setFocusItem(null) }
                                  }} />
                              ) : (
                                <button type="button" className="cellbtn" style={{ padding: 0, minHeight: 0 }}
                                  onClick={() => (phone() || !canEdit ? navigate(`/ws/${ws}/p/${row.item.id}`) : setTitleEdit({ item: row.item.id, draft: row.item.title }))}>
                                  <span className="vt"><span className="pic-i" aria-hidden>{paperIcon(row.item)}</span> {row.item.title || <span className="nil">{t('untitled')}</span>}</span>
                                </button>
                              )}
                              <Link className="open" to={`/ws/${ws}/p/${row.item.id}`}>{t('openItem')}</Link>
                            </div>
                          </td>
                          {props.map((p) => {
                            const v = shownValue(p, row.values.get(p.id))
                            const editingText = textEdit?.item === row.item.id && textEdit.prop === p.id
                            return (
                              <td key={p.id} className={cell?.item === row.item.id && cell.prop === p.id ? 'sel' : ''}>
                                {editingText ? (
                                  <input {...NO_AUTOFILL} className="cell-edit" autoFocus value={textEdit.draft} aria-label={p.name}
                                    onChange={(e) => setTextEdit({ ...textEdit, draft: e.target.value.replace(/\n/g, '') })}
                                    onBlur={() => { setValue(row.item.id, p.id, textEdit.draft.trim() === '' ? null : textEdit.draft); setTextEdit(null) }}
                                    onKeyDown={(e) => {
                                      if (isEnter(e)) { e.preventDefault(); e.currentTarget.blur() }
                                      if (e.key === 'Escape') setTextEdit(null)
                                    }} />
                                ) : (
                                  <button type="button" className="cellbtn" aria-label={`${row.item.title || t('untitled')} · ${p.name}`} disabled={!canEdit} onClick={(e) => openCell(row, p, e.currentTarget)}>
                                    {v === undefined ? <span className="nil">―</span> : <ValueView prop={p} value={v} />}
                                  </button>
                                )}
                              </td>
                            )
                          })}
                          <td className="add" />
                        </tr>
                      ))}
                      {loading && rows.length === 0 && [0, 1, 2].map((i) => (
                        <tr key={`sk${i}`} className="skelrow" aria-busy="true">
                          <td className="tt"><span className="skel" style={{ width: '60%' }} /></td>
                          {props.map((p) => <td key={p.id}><span className="skel" style={{ width: '50%' }} /></td>)}
                          <td className="add" />
                        </tr>
                      ))}
                      {next && (
                        <tr ref={sentinel} className="skelrow" aria-busy="true">
                          <td className="tt"><span className="skel" style={{ width: '60%' }} /></td>
                          <td colSpan={colSpan - 1} />
                        </tr>
                      )}
                      {canEdit && !next && (
                        <tr className="newrow">
                          <td className="tt" colSpan={1}><button type="button" onClick={() => void newItem()}>＋ {t('newItem')}</button></td>
                          <td colSpan={colSpan - 1} />
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
                {!loading && props.length === 0 && rows.length === 0 && <p className="dbhint">{t('dbEmptyHint')}</p>}
              </>
            )}
            {!db && loading && <div aria-busy="true"><div className="skel" style={{ width: '30%' }} /><div className="skel t" /></div>}
          </div>
        </div>
      </section>

      {menu && <Menu anchor={menu.anchor} items={menu.items} label={menu.label} onClose={() => setMenu(null)} />}
      {iconPick && db && (
        <IconPicker anchor={iconPick} value={db.icon ?? ''} onClose={() => setIconPick(null)} onPick={(icon) => {
          setIconPick(null)
          void send([{ op: 'set_icon', icon }]).then((r) => { if (r) onChanged() })
        }} />
      )}
      {adding && (
        <AddPropertyPopover anchor={adding} names={names} onClose={() => setAdding(null)}
          onAdd={(name, type) => void send([{ op: 'add_property', name, property_type: type }])} />
      )}
      {colMenu && editingProp && (
        <ColumnMenu prop={editingProp} anchor={colMenu.anchor} names={names}
          canLeft={props[0]?.id !== editingProp.id} canRight={props[props.length - 1]?.id !== editingProp.id}
          onRename={(name) => void send([{ op: 'update_property', property: editingProp.id, name }])}
          onMove={(dir) => {
            const ids = props.map((p) => p.id)
            const i = ids.indexOf(editingProp.id)
            const moved = moveAfter(ids, editingProp.id, dir < 0 ? ids[i - 2] : ids[i + 1])
            const at = moved.indexOf(editingProp.id)
            void send([{ op: 'move_property', property: editingProp.id, after: at === 0 ? '' : moved[at - 1] }])
          }}
          onDelete={() => setConfirmDelete(editingProp)}
          onClose={() => setColMenu(null)} />
      )}
      {confirmDelete && (
        <Modal title={t('deletePropertyTitle', { name: confirmDelete.name })} onClose={() => setConfirmDelete(null)}
          actions={[{ label: t('cancel') }, { label: t('deleteAction'), kind: 'danger solid', onClick: () => void send([{ op: 'delete_property', property: confirmDelete.id }]) }]}>
          <p>{t('deletePropertyBody', { name: confirmDelete.name })}</p>
        </Modal>
      )}
      {cell && cellRow && cellProp && (cellProp.type === 'select' || cellProp.type === 'multi_select') && (
        <ElementPicker prop={cellProp} value={shownValue(cellProp, cellRow.values.get(cellProp.id))} anchor={cell.anchor}
          onChange={(v) => setValue(cellRow.item.id, cellProp.id, v)} actions={elementActions(cellRow, cellProp)} onClose={() => setCell(null)} />
      )}
      {cell && cellRow && cellProp?.type === 'person' && (
        <PersonValuePicker ws={ws} value={asPeople(shownValue(cellProp, cellRow.values.get(cellProp.id)))} anchor={cell.anchor} label={cellProp.name}
          onChange={(v) => setValue(cellRow.item.id, cellProp.id, v)} onClose={() => setCell(null)} />
      )}
      {cell && cellRow && cellProp?.type === 'date' && (
        <DateEditor value={shownValue(cellProp, cellRow.values.get(cellProp.id)) as string | undefined} anchor={cell.anchor}
          onChange={(v) => setValue(cellRow.item.id, cellProp.id, v)} onClose={() => setCell(null)} />
      )}
      {cond && condOpen && condProp && (
        <ConditionEditor ws={ws} names={userNames} onPeople={rememberNames} prop={condProp} cond={condOpen} anchor={cond.anchor}
          onChange={(c) => setQuery(sort, filter.map((x, j) => (j === cond.index ? c : x)))}
          onRemove={() => { setQuery(sort, filter.filter((_, j) => j !== cond.index)); setCond(null) }}
          onClose={() => setCond(null)} />
      )}
    </>
  )
}

/** Edits one filter condition: its operator and what it compares with. */
function ConditionEditor({ ws, names, onPeople, prop, cond, anchor, onChange, onRemove, onClose }: {
  ws: string
  names: Map<string, string>
  onPeople: (people: PersonRef[]) => void
  prop: Property
  cond: FilterCondition
  anchor: Anchor
  onChange: (c: FilterCondition) => void
  onRemove: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [text, setText] = useState(cond.text ?? cond.date ?? '')
  const ops = OPS_OF[prop.type]
  const needs = cond.op !== 'is_empty' && cond.op !== 'is_not_empty'
  const setOp = (op: FilterCondition['op']) => {
    const base: FilterCondition = { property: prop.id, op }
    if (prop.type === 'person' && op === 'contains') base.users = cond.users ?? []
    else if (op === 'is' || op === 'contains') base.elements = cond.elements ?? []
    else if (op === 'starts_with') base.text = cond.text
    else if (op !== 'is_empty' && op !== 'is_not_empty') base.date = cond.date
    onChange(base)
  }
  const toggle = (el: Element) => {
    const cur = cond.elements ?? []
    const next = cur.includes(el.id) ? cur.filter((x) => x !== el.id) : [...cur, el.id].slice(0, 10)
    onChange({ ...cond, elements: next })
  }
  const commitText = () => {
    const v = text.trim()
    if (prop.type === 'date') onChange({ ...cond, date: /^\d{4}-\d{2}-\d{2}$/.test(v) ? v : undefined })
    else onChange({ ...cond, text: v || undefined })
  }
  return (
    <Popover anchor={anchor} onClose={onClose} className="dbpop" label={prop.name}>
      <div className="hd">{prop.name}</div>
      <select className="inp" value={cond.op} aria-label={t('filterItems')} onChange={(e) => setOp(e.target.value as FilterCondition['op'])}>
        {ops.map((op) => <option key={op} value={op}>{op === 'is' || op === 'contains' ? t('anyOf') : t(OP_LABEL[op])}</option>)}
      </select>
      {needs && prop.type === 'person' && (
        <PeopleChooser ws={ws} max={10} chosen={(cond.users ?? []).map((id) => ({ user_id: id, name: names.get(id) ?? '…' }))}
          onChange={(next) => { onPeople(next); onChange({ ...cond, users: next.map((p) => p.user_id) }) }} />
      )}
      {needs && prop.type !== 'person' && (cond.op === 'is' || cond.op === 'contains') && (prop.elements ?? []).map((el) => (
        <label key={el.id} className="opt">
          <input type="checkbox" checked={(cond.elements ?? []).includes(el.id)} onChange={() => toggle(el)} />
          <Chip el={el} />
        </label>
      ))}
      {needs && cond.op !== 'is' && cond.op !== 'contains' && (
        <input {...NO_AUTOFILL} className="inp" autoFocus value={text} placeholder={prop.type === 'date' ? 'YYYY-MM-DD' : prop.name} aria-label={prop.name}
          onChange={(e) => setText(e.target.value)} onBlur={commitText}
          onKeyDown={(e) => { if (isEnter(e)) { e.preventDefault(); commitText() } }} />
      )}
      <div className="msep" />
      <button type="button" className="opt" onClick={onRemove}>✕ {t('removeCondition')}</button>
    </Popover>
  )
}

