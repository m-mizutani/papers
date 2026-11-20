import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { Group, Role, RoleBinding, SubjectKind, User } from '../api/types'
import { useTranslation, type MsgKey } from '../i18n'

const ROLE_LABEL: Record<Exclude<Role, ''>, MsgKey> = { viewer: 'roleViewer', editor: 'roleEditor', owner: 'roleOwner' }
const KIND_LABEL: Record<SubjectKind, MsgKey> = { user: 'subjectUser', group: 'subjectGroup', domain: 'subjectDomain', authenticated: 'subjectAuthenticated' }

export function BindingsEditor({ value, onChange, roles }: { value: RoleBinding[]; onChange: (v: RoleBinding[]) => void; roles: Exclude<Role, ''>[] }) {
  const { t } = useTranslation()
  const [users, setUsers] = useState<User[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [kind, setKind] = useState<SubjectKind>('user')
  const [id, setId] = useState('')
  const [role, setRole] = useState<Exclude<Role, ''>>(roles[0] ?? 'viewer')

  useEffect(() => {
    void api.listUsers().then(setUsers)
    void api.listGroups().then(setGroups)
  }, [])

  const label = (b: RoleBinding) => {
    switch (b.subject.kind) {
      case 'user': {
        const u = users.find((x) => x.id === b.subject.id)
        return u ? `${u.name || u.email} (${u.email})` : b.subject.id
      }
      case 'group':
        return `${t('subjectGroup')}: ${groups.find((g) => g.id === b.subject.id)?.name ?? b.subject.id}`
      case 'domain':
        return `@${b.subject.id}`
      default:
        return t('subjectAuthenticated')
    }
  }

  const add = () => {
    const subject = kind === 'authenticated' ? { kind } : { kind, id: id.trim() }
    if (kind !== 'authenticated' && !subject.id) return
    const rest = value.filter((b) => !(b.subject.kind === subject.kind && (b.subject.id ?? '') === (subject.id ?? '')))
    onChange([...rest, { subject, role }])
    setId('')
  }

  return (
    <div>
      {value.map((b, i) => (
        <div className="binding" key={`${b.subject.kind}:${b.subject.id ?? ''}`}>
          <span>{label(b)}</span>
          <select className="field" value={b.role} aria-label={t('role')} onChange={(e) => onChange(value.map((x, j) => (j === i ? { ...x, role: e.target.value as Role } : x)))}>
            {roles.map((r) => <option key={r} value={r}>{t(ROLE_LABEL[r])}</option>)}
          </select>
          <button type="button" className="btn btn-quiet" onClick={() => onChange(value.filter((_, j) => j !== i))}>{t('remove')}</button>
        </div>
      ))}
      <div className="binding-add">
        <select className="field" value={kind} aria-label={t('subjectKind')} onChange={(e) => { setKind(e.target.value as SubjectKind); setId('') }}>
          {(Object.keys(KIND_LABEL) as SubjectKind[]).map((k) => <option key={k} value={k}>{t(KIND_LABEL[k])}</option>)}
        </select>
        {kind === 'user' && (
          <select className="field" value={id} aria-label={t('subjectUser')} onChange={(e) => setId(e.target.value)}>
            <option value="" />
            {users.map((u) => <option key={u.id} value={u.id}>{u.name || u.email}</option>)}
          </select>
        )}
        {kind === 'group' && (
          <select className="field" value={id} aria-label={t('subjectGroup')} onChange={(e) => setId(e.target.value)}>
            <option value="" />
            {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
          </select>
        )}
        {kind === 'domain' && <input className="field" value={id} placeholder="example.com" aria-label={t('subjectDomain')} onChange={(e) => setId(e.target.value)} />}
        {kind === 'authenticated' && <span />}
        <select className="field" value={role} aria-label={t('role')} onChange={(e) => setRole(e.target.value as Exclude<Role, ''>)}>
          {roles.map((r) => <option key={r} value={r}>{t(ROLE_LABEL[r])}</option>)}
        </select>
        <button type="button" className="btn" onClick={add}>{t('addPerson')}</button>
      </div>
    </div>
  )
}
