import { useCallback, useEffect, useState } from 'react'
import { ChevronRight, ChevronDown, File, Folder, FolderOpen } from 'lucide-react'
import type { FileEntry } from '../types/protocol'

interface Props {
  onFileSelect: (path: string) => void
}

interface TreeNode extends FileEntry {
  children?: TreeNode[]
  expanded?: boolean
  loaded?: boolean
}

export function FileTree({ onFileSelect }: Props) {
  const [nodes, setNodes] = useState<TreeNode[]>([])

  const loadDir = useCallback(async (dirPath: string): Promise<TreeNode[]> => {
    const res = await fetch(`/api/files?path=${encodeURIComponent(dirPath || '')}`)
    const files: FileEntry[] = await res.json()

    return files
      .sort((a, b) => {
        if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
        return a.name.localeCompare(b.name)
      })
      .map(f => ({ ...f, expanded: false, loaded: false }))
  }, [])

  useEffect(() => {
    loadDir('').then(setNodes)
  }, [loadDir])

  const toggleDir = useCallback(async (path: string) => {
    const toggle = async (items: TreeNode[]): Promise<TreeNode[]> => {
      const result: TreeNode[] = []
      for (const node of items) {
        if (node.path === path && node.is_dir) {
          if (!node.loaded) {
            const children = await loadDir(node.path)
            result.push({ ...node, expanded: true, loaded: true, children })
          } else {
            result.push({ ...node, expanded: !node.expanded })
          }
        } else if (node.children) {
          result.push({ ...node, children: await toggle(node.children) })
        } else {
          result.push(node)
        }
      }
      return result
    }
    setNodes(await toggle(nodes))
  }, [nodes, loadDir])

  const renderNodes = (items: TreeNode[], depth: number) => {
    return items.map(node => (
      <div key={node.path}>
        <div
          className="flex items-center gap-1 py-[3px] pr-2 cursor-pointer text-fg-muted whitespace-nowrap text-[12px] leading-snug select-none hover:bg-bg-hover hover:text-fg transition-colors"
          style={{ paddingLeft: 4 + depth * 12 }}
          onClick={() => node.is_dir ? toggleDir(node.path) : onFileSelect(node.path)}
          title={node.name}
        >
          <span className="w-3.5 flex items-center justify-center shrink-0 text-fg-dim">
            {node.is_dir ? (
              node.expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />
            ) : null}
          </span>
          <span className={`shrink-0 ${node.is_dir ? 'text-fg-muted' : 'text-fg-dim'}`}>
            {node.is_dir ? (
              node.expanded ? <FolderOpen size={14} /> : <Folder size={14} />
            ) : (
              <File size={13} />
            )}
          </span>
          <span className={`overflow-hidden text-ellipsis ml-0.5 ${node.is_dir ? 'text-fg' : ''}`}>{node.name}</span>
        </div>
        {node.expanded && node.children && renderNodes(node.children, depth + 1)}
      </div>
    ))
  }

  return <div className="flex-1 overflow-y-auto py-1 bg-bg">{renderNodes(nodes, 0)}</div>
}
