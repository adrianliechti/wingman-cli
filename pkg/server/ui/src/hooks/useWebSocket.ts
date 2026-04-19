import { useCallback, useEffect, useRef, useState } from 'react'
import type { ClientMessage, Phase, ServerMessage } from '../types/protocol'

export interface ChatEntry {
  id: string
  type: 'user' | 'assistant' | 'tool' | 'error'
  content: string
  toolName?: string
  toolArgs?: string
  toolHint?: string
  toolResult?: string
  toolId?: string
}

interface Usage {
  inputTokens: number
  outputTokens: number
}

export function useWebSocket() {
  const wsRef = useRef<WebSocket | null>(null)
  const [connected, setConnected] = useState(false)
  const [phase, setPhase] = useState<Phase>('idle')
  const [entries, setEntries] = useState<ChatEntry[]>([])
  const [usage, setUsage] = useState<Usage>({ inputTokens: 0, outputTokens: 0 })
  const [prompt, setPrompt] = useState<{ type: 'prompt' | 'ask'; question: string } | null>(null)

  const streamingRef = useRef<string>('')
  const streamingIdRef = useRef<string>('')
  const idCounterRef = useRef(0)

  const nextId = () => String(++idCounterRef.current)

  const addEntry = useCallback((entry: ChatEntry) => {
    setEntries(prev => [...prev, entry])
  }, [])

  const updateEntry = useCallback((id: string, updates: Partial<ChatEntry>) => {
    setEntries(prev => prev.map(e => e.id === id ? { ...e, ...updates } : e))
  }, [])

  const finalizeStreaming = useCallback(() => {
    if (streamingIdRef.current && streamingRef.current) {
      const id = streamingIdRef.current
      const content = streamingRef.current
      updateEntry(id, { content })
    }
    streamingRef.current = ''
    streamingIdRef.current = ''
  }, [updateEntry])

  const handleMessage = useCallback((msg: ServerMessage) => {
    switch (msg.type) {
      case 'messages': {
        const restored: ChatEntry[] = []
        for (const m of msg.messages) {
          for (const c of m.content) {
            if (c.text) {
              restored.push({ id: nextId(), type: m.role === 'user' ? 'user' : 'assistant', content: c.text })
            }
            if (c.tool_result) {
              restored.push({
                id: nextId(),
                type: 'tool',
                content: '',
                toolName: c.tool_result.name,
                toolArgs: c.tool_result.args,
                toolResult: c.tool_result.content,
              })
            }
          }
        }
        setEntries(restored)
        break
      }

      case 'text_delta': {
        if (!streamingIdRef.current) {
          const id = nextId()
          streamingIdRef.current = id
          streamingRef.current = ''
          addEntry({ id, type: 'assistant', content: '' })
        }
        streamingRef.current += msg.text
        const id = streamingIdRef.current
        const content = streamingRef.current
        updateEntry(id, { content })
        break
      }

      case 'tool_call': {
        finalizeStreaming()
        addEntry({
          id: msg.id || nextId(),
          type: 'tool',
          content: '',
          toolId: msg.id,
          toolName: msg.name,
          toolArgs: msg.args,
          toolHint: msg.hint,
        })
        break
      }

      case 'tool_result': {
        // Find the tool entry by toolId and update it
        setEntries(prev => {
          const idx = prev.findLastIndex(e => e.type === 'tool' && e.toolId === msg.id)
          if (idx >= 0) {
            const updated = [...prev]
            updated[idx] = { ...updated[idx], toolResult: msg.content }
            return updated
          }
          return prev
        })
        break
      }

      case 'phase':
        setPhase(msg.phase)
        break

      case 'prompt':
        setPrompt({ type: 'prompt', question: msg.question })
        break

      case 'ask':
        setPrompt({ type: 'ask', question: msg.question })
        break

      case 'error':
        finalizeStreaming()
        addEntry({ id: nextId(), type: 'error', content: msg.message })
        break

      case 'done':
        finalizeStreaming()
        break

      case 'usage':
        setUsage({ inputTokens: msg.input_tokens, outputTokens: msg.output_tokens })
        break
    }
  }, [addEntry, updateEntry, finalizeStreaming])

  const send = useCallback((msg: ClientMessage) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(msg))
    }
  }, [])

  const sendChat = useCallback((text: string, files?: string[]) => {
    addEntry({ id: nextId(), type: 'user', content: text })
    send({ type: 'send', text, files })
  }, [send, addEntry])

  const cancel = useCallback(() => {
    send({ type: 'cancel' })
  }, [send])

  const respondPrompt = useCallback((approved: boolean) => {
    send({ type: 'prompt_response', approved })
    setPrompt(null)
  }, [send])

  const respondAsk = useCallback((answer: string) => {
    send({ type: 'ask_response', answer })
    setPrompt(null)
  }, [send])

  useEffect(() => {
    let reconnectTimer: ReturnType<typeof setTimeout>

    function connect() {
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
      const ws = new WebSocket(`${proto}//${location.host}/ws/chat`)

      ws.onopen = () => {
        setConnected(true)
        setPhase('idle')
      }

      ws.onclose = () => {
        setConnected(false)
        wsRef.current = null
        reconnectTimer = setTimeout(connect, 2000)
      }

      ws.onerror = () => ws.close()

      ws.onmessage = (e) => {
        const msg: ServerMessage = JSON.parse(e.data)
        handleMessage(msg)
      }

      wsRef.current = ws
    }

    connect()

    return () => {
      clearTimeout(reconnectTimer)
      wsRef.current?.close()
    }
  }, [handleMessage])

  return {
    connected,
    phase,
    entries,
    usage,
    prompt,
    sendChat,
    cancel,
    respondPrompt,
    respondAsk,
    setEntries,
  }
}
