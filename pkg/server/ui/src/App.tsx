import { useCallback, useState } from 'react'
import { MessageSquare, FileText, GitCompare, X, PanelRightClose, PanelRightOpen } from 'lucide-react'
import { useWebSocket } from './hooks/useWebSocket'
import { Sidebar } from './components/Sidebar'
import { ChatPanel } from './components/ChatPanel'
import { DiffsPanel } from './components/DiffsPanel'
import { FileTree } from './components/FileTree'
import { FileTab } from './components/FileTab'
import { DiffTab } from './components/DiffTab'
import { PromptDialog } from './components/PromptDialog'
import { StatusBar } from './components/StatusBar'

interface CenterTab {
  id: string
  type: 'chat' | 'file' | 'diff'
  label: string
  path?: string // for file/diff tabs
}

type RightTab = 'changes' | 'files'

export default function App() {
  const { connected, phase, entries, usage, prompt, sendChat, cancel, respondPrompt, respondAsk, setEntries } = useWebSocket()
  const [sessionId, setSessionId] = useState('')
  const [rightTab, setRightTab] = useState<RightTab>('changes')
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [rightPanelCollapsed, setRightPanelCollapsed] = useState(false)

  // Center tabs: chat is always first, files are added dynamically
  const [tabs, setTabs] = useState<CenterTab[]>([{ id: 'chat', type: 'chat', label: 'Chat' }])
  const [activeTabId, setActiveTabId] = useState('chat')

  const openFile = useCallback((path: string) => {
    // Check if already open
    const existing = tabs.find(t => t.type === 'file' && t.path === path)
    if (existing) {
      setActiveTabId(existing.id)
      return
    }
    const label = path.split('/').pop() || path
    const tab: CenterTab = { id: `file:${path}`, type: 'file', label, path }
    setTabs(prev => [...prev, tab])
    setActiveTabId(tab.id)
  }, [tabs])

  const openDiff = useCallback((path: string) => {
    const existing = tabs.find(t => t.type === 'diff' && t.path === path)
    if (existing) {
      setActiveTabId(existing.id)
      return
    }
    const label = path.split('/').pop() || path
    const tab: CenterTab = { id: `diff:${path}`, type: 'diff', label, path }
    setTabs(prev => [...prev, tab])
    setActiveTabId(tab.id)
  }, [tabs])

  const closeTab = useCallback((id: string) => {
    if (id === 'chat') return // can't close chat
    setTabs(prev => prev.filter(t => t.id !== id))
    if (activeTabId === id) setActiveTabId('chat')
  }, [activeTabId])

  const handleNewSession = useCallback(async () => {
    const res = await fetch('/api/sessions/new', { method: 'POST' })
    const data = await res.json()
    setSessionId(data.id)
    setEntries([])
    setActiveTabId('chat')
  }, [setEntries])

  const handleSessionSelect = useCallback(async (id: string) => {
    const res = await fetch(`/api/sessions/${id}/load`, { method: 'POST' })
    if (!res.ok) return
    const messages = await res.json()
    const restored: Array<{ id: string; type: 'user' | 'assistant' | 'tool' | 'error'; content: string; toolName?: string; toolArgs?: string; toolResult?: string }> = []
    let counter = 0
    for (const m of messages) {
      for (const c of m.content) {
        counter++
        if (c.text) {
          restored.push({ id: String(counter), type: m.role === 'user' ? 'user' : 'assistant', content: c.text })
        }
        if (c.tool_result) {
          restored.push({ id: String(counter), type: 'tool', content: '', toolName: c.tool_result.name, toolArgs: c.tool_result.args, toolResult: c.tool_result.content })
        }
      }
    }
    setEntries(restored)
    setSessionId(id)
    setActiveTabId('chat')
  }, [setEntries])

  const activeTab = tabs.find(t => t.id === activeTabId) || tabs[0]

  return (
    <div className="flex flex-col h-screen bg-bg text-fg">
      <div className="flex flex-1 overflow-hidden">
        {/* Left Sidebar */}
        {!sidebarCollapsed && (
          <Sidebar currentSessionId={sessionId} onSessionSelect={handleSessionSelect} onNewSession={handleNewSession} />
        )}

        {/* Center Panel */}
        <div className="flex-1 flex flex-col overflow-hidden min-w-0 bg-bg">
          {/* Tab bar */}
          <div className="h-10 flex items-stretch bg-bg shrink-0 overflow-x-auto">
            {tabs.map(tab => {
              const active = tab.id === activeTabId
              const Icon = tab.type === 'chat' ? MessageSquare : tab.type === 'diff' ? GitCompare : FileText
              return (
                <div
                  key={tab.id}
                  className={`group relative flex items-center gap-1.5 px-3 cursor-pointer text-[12px] shrink-0 select-none transition-colors ${
                    active
                      ? 'text-fg'
                      : 'text-fg-dim hover:text-fg-muted'
                  }`}
                  onClick={() => setActiveTabId(tab.id)}
                >
                  {active && <span className="absolute bottom-0 left-2 right-2 h-[2px] bg-accent rounded-full" />}
                  <Icon size={13} className={active ? 'text-fg-muted' : 'text-fg-dim'} />
                  <span className="truncate max-w-[200px]">{tab.label}</span>
                  {tab.type === 'chat' ? (
                    <span className="w-4" />
                  ) : (
                    <button
                      className="w-4 h-4 flex items-center justify-center text-fg-dim hover:text-fg rounded opacity-0 group-hover:opacity-100 transition-opacity"
                      onClick={e => { e.stopPropagation(); closeTab(tab.id) }}
                      aria-label="Close tab"
                    >
                      <X size={11} />
                    </button>
                  )}
                </div>
              )
            })}
            <div className="flex-1" />
            <button
              className="flex items-center justify-center w-10 h-10 text-fg-dim hover:text-fg-muted cursor-pointer transition-colors shrink-0"
              onClick={() => setRightPanelCollapsed(c => !c)}
              title={rightPanelCollapsed ? 'Show panel' : 'Hide panel'}
            >
              {rightPanelCollapsed ? <PanelRightOpen size={15} /> : <PanelRightClose size={15} />}
            </button>
          </div>

          {/* Divider */}
          <div className="h-px bg-border-subtle shrink-0" />

          {/* Tab content */}
          <div className="flex-1 overflow-hidden">
            {activeTab.type === 'chat' ? (
              <ChatPanel entries={entries} phase={phase} onSend={sendChat} onCancel={cancel} />
            ) : activeTab.type === 'diff' && activeTab.path ? (
              <DiffTab path={activeTab.path} />
            ) : activeTab.path ? (
              <FileTab path={activeTab.path} />
            ) : null}
          </div>
        </div>

        {/* Right Panel */}
        {!rightPanelCollapsed && (
          <div className="w-72 border-l border-border-subtle flex flex-col bg-bg shrink-0">
            <div className="h-10 flex items-stretch shrink-0">
              <RightTabButton active={rightTab === 'changes'} onClick={() => setRightTab('changes')}>Changes</RightTabButton>
              <RightTabButton active={rightTab === 'files'} onClick={() => setRightTab('files')}>Files</RightTabButton>
              <div className="flex-1" />
            </div>
            <div className="h-px bg-border-subtle shrink-0" />
            <div className="flex-1 overflow-hidden">
              {rightTab === 'changes' ? (
                <DiffsPanel visible={true} onOpenDiff={openDiff} />
              ) : (
                <FileTree onFileSelect={openFile} />
              )}
            </div>
          </div>
        )}
      </div>

      <StatusBar
        connected={connected}
        phase={phase}
        inputTokens={usage.inputTokens}
        outputTokens={usage.outputTokens}
        onToggleSidebar={() => setSidebarCollapsed(c => !c)}
      />

      <PromptDialog prompt={prompt} onPromptResponse={respondPrompt} onAskResponse={respondAsk} />
    </div>
  )
}

function RightTabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      className={`relative px-4 text-[11px] font-medium cursor-pointer transition-colors ${
        active ? 'text-fg' : 'text-fg-dim hover:text-fg-muted'
      }`}
      onClick={onClick}
    >
      {children}
      {active && <span className="absolute left-3 right-3 bottom-0 h-[2px] bg-accent rounded-full" />}
    </button>
  )
}
