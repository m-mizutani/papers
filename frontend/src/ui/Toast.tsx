import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'

export interface ToastAction {
  label: string
  run: () => void
}

type Show = (message: string, action?: ToastAction) => void

const Ctx = createContext<Show>(() => undefined)

/** Short messages at the bottom; one with an action stays a little longer. */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<{ id: number; message: string; action?: ToastAction }[]>([])
  const show = useCallback<Show>((message, action) => {
    const id = Date.now() + Math.random()
    setItems((xs) => [...xs, { id, message, action }])
    window.setTimeout(() => setItems((xs) => xs.filter((x) => x.id !== id)), action ? 6000 : 3200)
  }, [])
  return (
    <Ctx.Provider value={show}>
      {children}
      <div className="toasts" aria-live="polite">
        {items.map((it) => (
          <div key={it.id} className="toast" role="status">
            <span>{it.message}</span>
            {it.action && (
              <button type="button" onClick={() => { it.action?.run(); setItems((xs) => xs.filter((x) => x.id !== it.id)) }}>{it.action.label}</button>
            )}
          </div>
        ))}
      </div>
    </Ctx.Provider>
  )
}

export function useToast(): Show {
  return useContext(Ctx)
}
