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
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-200">
      <div className="bg-bg-surface border border-border rounded-lg p-6 max-w-[500px] w-[90%]">
        <div className="mb-4 leading-normal whitespace-pre-wrap">{prompt.question}</div>
        {prompt.type === 'prompt' ? (
          <div className="flex gap-2 justify-end">
            <button
              className="px-4 py-1.5 rounded border border-border bg-bg text-fg cursor-pointer font-inherit text-xs hover:opacity-90"
              onClick={() => onPromptResponse(false)}
            >
              Deny
            </button>
            <button
              className="px-4 py-1.5 rounded border border-accent bg-accent text-bg cursor-pointer font-inherit text-xs hover:opacity-90"
              onClick={() => onPromptResponse(true)}
            >
              Allow
            </button>
          </div>
        ) : (
          <div>
            <input
              className="w-full bg-bg border border-border rounded px-2.5 py-2 text-fg font-inherit text-[13px] mb-3 outline-none focus:border-accent"
              value={answer}
              onChange={e => setAnswer(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') handleAskSubmit() }}
              placeholder="Type your answer..."
              autoFocus
            />
            <div className="flex gap-2 justify-end">
              <button
                className="px-4 py-1.5 rounded border border-accent bg-accent text-bg cursor-pointer font-inherit text-xs hover:opacity-90"
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
