import { useCallback, useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import type { DiffEntry } from '../types/protocol'

interface Props {
  visible: boolean
  onOpenDiff?: (path: string) => void
}

export function DiffsPanel({ visible, onOpenDiff }: Props) {
  const [diffs, setDiffs] = useState<DiffEntry[]>([])

  const loadDiffs = useCallback(async () => {
    try {
      const res = await fetch('/api/diffs')
      if (!res.ok) { setDiffs([]); return }
      const data: DiffEntry[] = await res.json()
      setDiffs(data)
    } catch {
      setDiffs([])
    }
  }, [])

  useEffect(() => {
    if (visible) loadDiffs()
  }, [visible, loadDiffs])

  if (!visible) return null

  const statusLabels: Record<string, string> = { added: 'A', modified: 'M', deleted: 'D' }
  const statusColors: Record<string, string> = { added: 'text-success', modified: 'text-warning', deleted: 'text-danger' }

  return (
    <div className="flex flex-col h-full overflow-hidden bg-bg">
      {/* Header */}
      <div className="h-8 px-3 flex items-center justify-between shrink-0">
        <span className="text-[11px] text-fg-muted">
          {diffs.length === 0 ? 'No changes' : `${diffs.length} ${diffs.length === 1 ? 'file' : 'files'} changed`}
        </span>
        <button
          className="w-5 h-5 flex items-center justify-center rounded text-fg-muted hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors"
          onClick={loadDiffs}
          title="Refresh"
        >
          <RefreshCw size={12} />
        </button>
      </div>
      <div className="overflow-y-auto flex-1 px-1 pb-2">
        {diffs.length === 0 && (
          <div className="px-3 py-6 text-[11px] text-fg-dim text-center">No changes yet</div>
        )}
        {diffs.map(diff => {
          const fileName = diff.path.split('/').pop() || diff.path
          const dir = diff.path.slice(0, diff.path.length - fileName.length).replace(/\/$/, '')
          return (
            <div
              key={diff.path}
              className="group flex items-center gap-2 mx-1 px-2 py-1.5 rounded cursor-pointer text-[12px] text-fg-muted hover:bg-bg-hover hover:text-fg transition-colors"
              onClick={() => onOpenDiff?.(diff.path)}
              title={diff.path}
            >
              <span className={`text-[10.5px] font-bold w-3 text-center shrink-0 ${statusColors[diff.status]}`}>
                {statusLabels[diff.status]}
              </span>
              <span className="truncate font-mono text-[11.5px]">{fileName}</span>
              {dir && <span className="text-fg-dim truncate font-mono text-[10.5px] ml-auto">{dir}</span>}
            </div>
          )
        })}
      </div>
    </div>
  )
}
