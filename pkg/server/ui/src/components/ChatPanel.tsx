import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowUp, Square, ChevronRight, ChevronDown } from 'lucide-react'
import type { ChatEntry } from '../hooks/useWebSocket'
import type { Phase } from '../types/protocol'

interface Props {
  entries: ChatEntry[]
  phase: Phase
  onSend: (text: string) => void
  onCancel: () => void
}

export function ChatPanel({ entries, phase, onSend, onCancel }: Props) {
  const [input, setInput] = useState('')
  const messagesRef = useRef<HTMLDivElement>(null)

  const isActive = phase !== 'idle'

  useEffect(() => {
    const el = messagesRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [entries])

  const handleSubmit = useCallback(() => {
    const text = input.trim()
    if (!text || isActive) return
    onSend(text)
    setInput('')
  }, [input, isActive, onSend])

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSubmit()
    }
    if (e.key === 'Escape' && isActive) {
      onCancel()
    }
  }, [handleSubmit, isActive, onCancel])

  return (
    <div className="h-full relative overflow-hidden bg-bg">
      <div className="h-full overflow-y-auto pb-24" ref={messagesRef}>
        {entries.length === 0 ? (
          <div className="h-full flex items-center justify-center">
            <div className="text-center max-w-sm">
              <div className="text-[28px] font-semibold text-fg mb-2">Wingman</div>
              <div className="text-[13px] text-fg-dim leading-relaxed">
                Ask me to write code, fix bugs, explore files, or run commands.
              </div>
            </div>
          </div>
        ) : (
          <div className="px-4 py-4">
            {entries.map(entry => (
              <EntryView
                key={entry.id}
                entry={entry}
                isStreaming={phase === 'streaming' && entry === entries[entries.length - 1] && entry.type === 'assistant'}
              />
            ))}
          </div>
        )}
      </div>

      {/* Floating input */}
      <div className="absolute bottom-0 left-0 right-0">
        <div className="h-8 bg-gradient-to-t from-bg to-transparent pointer-events-none" />
        <div className="bg-bg px-4 pb-4">
          <div className="flex items-end gap-2">
            <div className="flex-1 flex items-end bg-bg-surface border border-border-subtle rounded-xl focus-within:border-border-strong transition-colors">
              <textarea
                className="flex-1 bg-transparent px-4 py-3 text-fg text-[13px] resize-none outline-none min-h-[44px] max-h-[200px] leading-normal placeholder:text-fg-dim"
                style={{ fieldSizing: 'content' } as React.CSSProperties}
                value={input}
                onChange={e => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder="Message Wingman..."
                rows={1}
              />
            </div>
            {isActive ? (
              <button
                className="h-[44px] w-[44px] flex items-center justify-center rounded-xl text-fg-muted bg-bg-surface border border-border-subtle cursor-pointer hover:text-fg hover:border-border-strong transition-colors"
                onClick={onCancel}
                title="Stop (Esc)"
              >
                <Square size={14} fill="currentColor" />
              </button>
            ) : (
              <button
                className="h-[44px] w-[44px] flex items-center justify-center rounded-xl bg-bg-surface border border-border-subtle text-fg-muted cursor-pointer hover:text-fg hover:border-border-strong disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                onClick={handleSubmit}
                disabled={!input.trim()}
                title="Send (Enter)"
              >
                <ArrowUp size={16} />
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

function EntryView({ entry, isStreaming }: { entry: ChatEntry; isStreaming: boolean }) {
  if (entry.type === 'error') {
    return (
      <div className="mb-4 border-l-2 border-danger pl-3">
        <div className="text-[13px] leading-relaxed text-danger break-words">{entry.content}</div>
      </div>
    )
  }

  if (entry.type === 'tool') {
    return <ToolCallView entry={entry} />
  }

  const isUser = entry.type === 'user'

  return (
    <div className={`mb-4 border-l-2 ${isUser ? 'border-success' : 'border-purple'} pl-3`}>
      <div className="text-[12px] leading-[1.7] break-words min-w-0 font-mono">
        {isUser ? (
          <span className="whitespace-pre-wrap text-fg">{entry.content}</span>
        ) : (
          <>
            <MarkdownContent text={entry.content} />
            {isStreaming && (
              <span className="inline-block w-[6px] h-[14px] bg-fg-dim align-text-bottom ml-0.5 animate-[blink_1s_step-end_infinite]" />
            )}
          </>
        )}
      </div>
    </div>
  )
}

function ToolCallView({ entry }: { entry: ChatEntry }) {
  const [expanded, setExpanded] = useState(false)
  const hint = entry.toolHint || extractHint(entry.toolArgs)
  const displayHint = hint ? truncate(hint, 80) : ''

  return (
    <div className="mb-4 border-l-2 border-purple pl-3">
      <div
        className="flex items-center gap-2 py-0.5 cursor-pointer text-[12px] transition-colors"
        onClick={() => setExpanded(!expanded)}
      >
        <span className="text-fg-dim shrink-0 flex items-center">
          {expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </span>
        <span className="text-purple font-mono text-[11px] shrink-0">{entry.toolName}</span>
        {displayHint && (
          <span className="text-fg-dim font-mono text-[11px] overflow-hidden text-ellipsis whitespace-nowrap flex-1">
            {displayHint}
          </span>
        )}
      </div>
      {expanded && (
        <div className="mt-1 px-3 py-2 text-[11px] max-h-[320px] overflow-y-auto whitespace-pre-wrap break-all text-fg-dim bg-bg-surface rounded-md font-mono leading-relaxed">
          {truncate(entry.toolResult || '(no output)', 2000)}
        </div>
      )}
    </div>
  )
}

function MarkdownContent({ text }: { text: string }) {
  const html = renderMarkdown(text)
  return <div dangerouslySetInnerHTML={{ __html: html }} />
}

function renderMarkdown(text: string): string {
  if (!text) return ''

  let html = escapeHtml(text)

  // Code blocks
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_, _lang, code) => {
    return `<pre style="background:var(--color-bg-surface);border:1px solid var(--color-border-subtle);border-radius:6px;padding:10px 12px;margin:8px 0;overflow-x:auto;font-family:var(--font-mono);font-size:12px;line-height:1.55"><code>${code.trim()}</code></pre>`
  })

  // Inline code
  html = html.replace(/`([^`]+)`/g, '<code style="background:var(--color-bg-surface);border:1px solid var(--color-border-subtle);padding:1px 5px;border-radius:4px;font-family:var(--font-mono);font-size:11.5px">$1</code>')

  // Headers
  html = html.replace(/^###\s+(.+)$/gm, '<h3 style="margin:14px 0 6px;font-weight:600;font-size:13px;color:var(--color-fg)">$1</h3>')
  html = html.replace(/^##\s+(.+)$/gm, '<h2 style="margin:16px 0 6px;font-weight:600;font-size:14px;color:var(--color-fg)">$1</h2>')
  html = html.replace(/^#\s+(.+)$/gm, '<h1 style="margin:18px 0 8px;font-weight:600;font-size:15px;color:var(--color-fg)">$1</h1>')

  // Bold/italic
  html = html.replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>')
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong style="color:var(--color-fg);font-weight:600">$1</strong>')
  html = html.replace(/\*(.+?)\*/g, '<em style="color:var(--color-fg-muted)">$1</em>')

  // Links
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener" style="color:var(--color-accent);text-decoration:none">$1</a>')

  // Lists
  html = html.replace(/^[\-\*]\s+(.+)$/gm, '<li style="margin-bottom:3px">$1</li>')
  html = html.replace(/(<li[\s\S]*?<\/li>)/g, '<ul style="margin:6px 0;padding-left:20px">$1</ul>')
  html = html.replace(/<\/ul>\s*<ul[^>]*>/g, '')

  // Blockquotes
  html = html.replace(/^&gt;\s+(.+)$/gm, '<blockquote style="border-left:2px solid var(--color-border);padding-left:10px;color:var(--color-fg-muted);margin:6px 0">$1</blockquote>')
  html = html.replace(/<\/blockquote>\s*<blockquote[^>]*>/g, '<br>')

  // Paragraphs
  html = html.replace(/\n\n/g, '</p><p style="margin-bottom:8px">')
  html = `<p style="margin-bottom:8px">${html}</p>`
  html = html.replace(/([^>])\n([^<])/g, '$1<br>$2')
  html = html.replace(/<p[^>]*>\s*<\/p>/g, '')

  return html
}

function escapeHtml(text: string): string {
  const el = document.createElement('div')
  el.textContent = text
  return el.innerHTML
}

function extractHint(argsJSON?: string): string {
  if (!argsJSON) return ''
  try {
    const args = JSON.parse(argsJSON)
    for (const key of ['description', 'query', 'pattern', 'command', 'prompt', 'path', 'file', 'url', 'name']) {
      if (typeof args[key] === 'string' && args[key]) return args[key]
    }
  } catch { /* ignore */ }
  return ''
}

function truncate(text: string, max: number): string {
  return text.length <= max ? text : text.substring(0, max) + '...'
}
