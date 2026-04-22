import { useEffect, useState } from 'react'
import type { Phase } from '../types/protocol'

interface Props {
  connected: boolean
  phase: Phase
  inputTokens: number
  outputTokens: number
  onToggleSidebar: () => void
}

export function StatusBar({ connected, phase, inputTokens, outputTokens, onToggleSidebar }: Props) {
  const [model, setModel] = useState('')

  useEffect(() => {
    fetch('/api/model')
      .then(r => r.json())
      .then(data => setModel(data.model || ''))
      .catch(() => {})
  }, [])

  const statusText = () => {
    if (!connected) return 'Disconnected'
    switch (phase) {
      case 'thinking': return 'Thinking...'
      case 'streaming': return 'Streaming...'
      case 'tool_running': return 'Running tool...'
      default: return 'Ready'
    }
  }

  const indicatorColor = () => {
    if (!connected) return 'bg-fg-dim'
    switch (phase) {
      case 'thinking': return 'bg-warning animate-[pulse_1s_infinite]'
      case 'streaming': return 'bg-accent animate-[pulse_0.5s_infinite]'
      case 'tool_running': return 'bg-orange animate-[pulse_0.8s_infinite]'
      default: return 'bg-success'
    }
  }

  return (
    <div className="flex justify-between items-center h-7 px-3 border-t border-border-subtle bg-bg text-[11px] text-fg-dim shrink-0">
      <div className="flex items-center gap-2.5">
        <button
          className="text-fg-dim cursor-pointer text-sm hover:text-fg-muted p-0.5"
          onClick={onToggleSidebar}
          title="Toggle sidebar"
        >
          {'\u2630'}
        </button>
        <div className={`w-1.5 h-1.5 rounded-full ${indicatorColor()}`} />
        <span>{statusText()}</span>
      </div>
      <div className="flex items-center gap-3">
        {model && <span>{model}</span>}
        {(inputTokens > 0 || outputTokens > 0) && (
          <span className="tabular-nums">{'\u2191'}{formatTokens(inputTokens)} {'\u2193'}{formatTokens(outputTokens)}</span>
        )}
      </div>
    </div>
  )
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return String(n)
}
