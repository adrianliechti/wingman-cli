import { useCallback, useEffect, useState } from 'react'
import { Plus, X, MessageSquare } from 'lucide-react'

interface SessionInfo {
  id: string
  created_at: string
  updated_at: string
}

interface Props {
  currentSessionId: string
  onSessionSelect: (id: string) => void
  onNewSession: () => void
}

export function Sidebar({ currentSessionId, onSessionSelect, onNewSession }: Props) {
  const [sessions, setSessions] = useState<SessionInfo[]>([])

  const loadSessions = useCallback(async () => {
    try {
      const res = await fetch('/api/sessions')
      const data: SessionInfo[] = await res.json()
      setSessions(data)
    } catch {
      setSessions([])
    }
  }, [])

  useEffect(() => {
    loadSessions()
    const interval = setInterval(loadSessions, 10000)
    return () => clearInterval(interval)
  }, [loadSessions])

  // Reload when session changes
  useEffect(() => {
    loadSessions()
  }, [currentSessionId, loadSessions])

  const handleDelete = async (e: React.MouseEvent, id: string) => {
    e.stopPropagation()
    await fetch(`/api/sessions/${id}`, { method: 'DELETE' })
    loadSessions()
  }

  return (
    <div className="w-60 min-w-[220px] flex flex-col bg-bg-surface shrink-0 border-r border-border-subtle">
      {/* Header */}
      <div className="h-9 px-3 flex items-center justify-between shrink-0">
        <span className="text-[11px] font-semibold uppercase tracking-wider text-fg-muted">Sessions</span>
        <button
          onClick={onNewSession}
          className="w-5 h-5 flex items-center justify-center rounded text-fg-muted hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors"
          title="New session"
        >
          <Plus size={14} />
        </button>
      </div>

      {/* Session List */}
      <div className="flex-1 overflow-y-auto px-1 pb-2">
        {sessions.map(s => {
          const active = s.id === currentSessionId
          return (
            <div
              key={s.id}
              className={`group relative flex items-center gap-2 px-2 py-1.5 mx-1 rounded cursor-pointer text-[12px] transition-colors ${
                active
                  ? 'bg-bg-active text-fg'
                  : 'text-fg-muted hover:bg-bg-hover hover:text-fg'
              }`}
              onClick={() => onSessionSelect(s.id)}
            >
              <MessageSquare size={13} className="shrink-0 text-fg-dim" />
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium font-mono text-[12px]">{s.id.substring(0, 8)}</div>
                <div className="text-[10.5px] text-fg-dim truncate mt-0.5">{formatDate(s.updated_at)}</div>
              </div>
              <button
                onClick={(e) => handleDelete(e, s.id)}
                className="w-5 h-5 flex items-center justify-center rounded text-fg-dim hover:text-danger hover:bg-bg-active opacity-0 group-hover:opacity-100 shrink-0 transition-all"
                title="Delete session"
              >
                <X size={12} />
              </button>
            </div>
          )
        })}
        {sessions.length === 0 && (
          <div className="px-3 py-6 text-[11px] text-fg-dim text-center">No sessions yet</div>
        )}
      </div>
    </div>
  )
}

function formatDate(value: string): string {
  if (!value) return ''
  const d = new Date(value)
  if (isNaN(d.getTime())) return value
  const now = new Date()
  const sameDay = d.toDateString() === now.toDateString()
  if (sameDay) {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}
