import { useEffect, useState } from 'react'

export type ToastVariant = 'default' | 'success' | 'error'

export interface ToastItem {
  id: number
  message: string
  variant: ToastVariant
}

let nextId = 1
let items: ToastItem[] = []
const listeners = new Set<(items: ToastItem[]) => void>()

function emit() {
  for (const listener of listeners) listener(items)
}

function dismiss(id: number) {
  items = items.filter((t) => t.id !== id)
  emit()
}

export function toast(message: string, variant: ToastVariant = 'default') {
  const id = nextId++
  items = [...items, { id, message, variant }]
  emit()
  setTimeout(() => dismiss(id), 4000)
  return id
}

toast.success = (message: string) => toast(message, 'success')
toast.error = (message: string) => toast(message, 'error')

export function useToasts(): { items: ToastItem[]; dismiss: (id: number) => void } {
  const [state, setState] = useState(items)

  useEffect(() => {
    listeners.add(setState)
    return () => {
      listeners.delete(setState)
    }
  }, [])

  return { items: state, dismiss }
}
