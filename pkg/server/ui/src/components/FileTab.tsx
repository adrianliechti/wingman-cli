import { useEffect, useState } from 'react'
import Editor from '@monaco-editor/react'
import type { FileContent } from '../types/protocol'

interface Props {
  path: string
}

export function FileTab({ path }: Props) {
  const [file, setFile] = useState<FileContent | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    fetch(`/api/files/read?path=${encodeURIComponent(path)}`)
      .then(r => r.json())
      .then((data: FileContent) => { setFile(data); setLoading(false) })
      .catch(() => setLoading(false))
  }, [path])

  if (loading) {
    return <div className="flex items-center justify-center h-full text-fg-dim text-sm">Loading...</div>
  }

  if (!file) {
    return <div className="flex items-center justify-center h-full text-fg-dim text-sm">Failed to load file</div>
  }

  return (
    <Editor
      height="100%"
      language={file.language || undefined}
      value={file.content}
      theme="vs-dark"
      options={{
        readOnly: true,
        minimap: { enabled: false },
        fontSize: 12,
        lineNumbers: 'on',
        scrollBeyondLastLine: false,
        wordWrap: 'on',
        renderWhitespace: 'none',
        padding: { top: 8 },
      }}
    />
  )
}
