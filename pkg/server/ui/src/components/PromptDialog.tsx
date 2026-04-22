import { useState } from 'react'

interface Props {
  prompt: { type: 'prompt' | 'ask'; question: string } | null
  onPromptResponse: (approved: boolean) => void
  onAskResponse: (answer: string) => void
}

export function PromptDialog({ prompt, onPromptResponse, onAskResponse }: Props) {
  const [answer, setAnswer] = useState('')

  if (!prompt) return null

  const handleAskSubmit = () => {
    onAskResponse(answer)
    setAnswer('')
  }

  return (
    <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-200 backdrop-blur-xs">
      <div className="bg-bg-elevated border border-border-subtle rounded-xl p-6 max-w-[500px] w-[90%] shadow-2xl">
        <div className="mb-4 text-[13px] leading-relaxed whitespace-pre-wrap text-fg">{prompt.question}</div>
        {prompt.type === 'prompt' ? (
          <div className="flex gap-2 justify-end">
            <button
              className="px-4 py-1.5 rounded-lg bg-bg-surface text-fg-muted cursor-pointer font-inherit text-[12px] hover:text-fg hover:bg-bg-active transition-colors"
              onClick={() => onPromptResponse(false)}
            >
              Deny
            </button>
            <button
              className="px-4 py-1.5 rounded-lg bg-accent text-white cursor-pointer font-inherit text-[12px] hover:bg-accent-hover transition-colors"
              onClick={() => onPromptResponse(true)}
            >
              Allow
            </button>
          </div>
        ) : (
          <div>
            <input
              className="w-full bg-bg-surface border border-border-subtle rounded-lg px-3 py-2.5 text-fg font-inherit text-[13px] mb-3 outline-none focus:border-border-strong transition-colors"
              value={answer}
              onChange={e => setAnswer(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') handleAskSubmit() }}
              placeholder="Type your answer..."
              autoFocus
            />
            <div className="flex gap-2 justify-end">
              <button
                className="px-4 py-1.5 rounded-lg bg-accent text-white cursor-pointer font-inherit text-[12px] hover:bg-accent-hover transition-colors"
                onClick={handleAskSubmit}
              >
                Submit
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
