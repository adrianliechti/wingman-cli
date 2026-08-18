# Smart Editor Feature Gaps

Comparison captured on 2026-08-15 against VS Code, Cursor, AWS Kiro, and Google Antigravity.

Kiro is an AWS product. Google Antigravity 2.0 is positioned more as a standalone agent command center than as a conventional editor.

## Current strengths

Wingman already provides:

- Monaco-based editing with HTML, SVG, Markdown, Mermaid, and structured-data previews.
- Zero-config detection of ~50 language servers with LSP completion, navigation, references, rename, code actions, semantic tokens, formatting, diagnostics, and inlay hints, plus a tree-sitter fallback for navigation, hover, and completion when no server covers a file.
- Revision-checked file writes with atomic multi-file batches and post-edit diagnostics fed back to the agent.
- Streaming workspace search with regex, case, whole-word, and file filters, plus previewed revision-checked replacement at match, file, and workspace scope.
- File editing, terminals, permission gates, multiple models, persistent agent sessions, and queued or steerable turns.
- Git status, branches, comparisons, staging, commits, pull, and push.
- Tasks and schedules, skills, plugins, MCP, lifecycle hooks, structural code intelligence, and subagents.

## Feature gaps

### P0: AI inline editing

Add:

- Ghost-text completion.
- Next-edit prediction and Tab-to-jump navigation.
- Coordinated multi-line and multi-file suggestions.
- Selection-based inline chat and transformations.
- Awareness of recent edits, diagnostics, and surrounding files.
- Terminal command generation and terminal-output-to-chat context.
- AI-generated commit messages in the commit box.

Benchmarks: [Cursor Tab](https://cursor.com/help/ai-features/tab) and [VS Code AI-powered suggestions](https://code.visualstudio.com/docs/editing/ai-powered-suggestions).

### P0: Agent checkpoints and granular review

Add:

- Automatic checkpoints before agent changes.
- Rewind, redo, and fork from a previous turn.
- Per-turn change provenance.
- Accept or reject individual files, hunks, and lines.
- A safe merge flow for partially accepted agent changes.

Constraint: must be native git only — the shadow-repo approach was tried and removed; non-git folders need explicit init.

Benchmarks: [VS Code chat checkpoints](https://code.visualstudio.com/docs/chat/chat-checkpoints) and [Cursor Agent](https://cursor.com/docs/agent/overview).

### P0: Isolated parallel agents

Subagents currently share the same working tree, which can cause conflicting edits. Add:

- A Git worktree per agent or session.
- Conflict detection and a merge-back workflow.
- Parallel session management across workspaces.
- Optional isolated cloud sandboxes for asynchronous or offline work.
- Multi-repository sessions and remote monitoring.

Benchmarks: [VS Code agent sessions](https://code.visualstudio.com/docs/agents/agents-window), [Cursor Cloud Agents](https://cursor.com/docs/cloud-agent), [Kiro Web](https://kiro.dev/docs/web/using-the-agent/), and [Antigravity projects](https://www.antigravity.google/docs/features).

### P1: Browser and visual-development agent

Add:

- An agent-controlled browser and Chrome DevTools integration.
- DOM element selection and visual annotations.
- Screenshot and video capture for verification.
- Visual regression and responsive-layout checks.
- Design-mode changes based on selected page elements.
- Optional voice input for visual editing workflows.

Benchmarks: [Cursor Design Mode](https://cursor.com/docs/agent/design-mode) and [Antigravity features](https://www.antigravity.google/docs/features).

### P1: Full IDE debugging and testing

Add:

- Debug Adapter Protocol support.
- Breakpoints, variables, watches, call stacks, and a debug console.
- Test discovery and a Test Explorer.
- Run and debug controls for individual tests and suites.
- Coverage visualization and inline results.
- Notebook support where applicable.

Benchmarks: [VS Code debugging](https://code.visualstudio.com/docs/debugtest/debugging) and [VS Code testing](https://code.visualstudio.com/docs/debugtest/testing).

### P1: Structured specifications and artifacts

Plan mode exists, but specifications are not first-class project objects. Add:

- Structured requirements, design, and task documents.
- Acceptance criteria and traceability from requirement to implementation.
- Dependency-aware tasks with tracked execution state.
- Reviewable plans and implementation artifacts with inline comments.
- Optional property-based correctness criteria.

Benchmark: [Kiro Specs](https://kiro.dev/docs/specs/).

### P1: Editor ergonomics and personalization

Add:

- Split view and editor groups; tab drag-reorder and pinning.
- Persisted editor state (open tabs, layout, panel sizes) across reloads.
- A settings UI, configurable keybindings, and a theme picker.
- Outline view and breadcrumbs.
- Go to Symbol in Workspace — the capability is advertised but no HTTP route exists (`server/handler_lsp.go`).
- Notebook (.ipynb) support.

Benchmark: [VS Code user interface](https://code.visualstudio.com/docs/getstarted/userinterface).

### P1: Advanced Git and collaboration

Add:

- A three-way merge-conflict editor.
- Hunk- and line-level staging, amend, and git status decorations in the file tree.
- Stash, rebase, cherry-pick, and worktree management.
- Blame and file timeline views.
- Pull request and issue integration.
- Automated pull-request review and suggested fixes.
- Shared agent sessions and team review flows.

Benchmarks: [VS Code source control](https://code.visualstudio.com/docs/sourcecontrol/overview) and [Cursor documentation](https://cursor.com/docs).

### P1: Semantic context and passive memory

The existing structural code graph is a strong foundation. Add:

- Embedding-based natural-language repository search.
- Indexed documentation, issues, pull requests, and history.
- Automatically proposed project memories with user approval.
- Project-scoped memory management and deletion controls.
- Context freshness and index-status indicators.

Benchmark: [Cursor Memories](https://cursor.com/docs/context/memories).

### P2: Extension and plugin distribution

Plugins, skills, MCP, and hooks exist, but distribution is mostly manual. Add:

- A searchable marketplace.
- One-click installation and OAuth connection flows.
- Dependency, version, compatibility, and update management.
- Dynamic on-demand activation of plugins and MCP servers.
- Ratings, trust indicators, permission declarations, and publisher verification.

Benchmarks: [VS Code Extension Marketplace](https://code.visualstudio.com/docs/configure/extensions/extension-marketplace) and [Kiro Powers](https://kiro.dev/docs/powers/).

### P2: Remote and cloud development

The initial runtime boundary is intentionally small: one `wingman server`
process lives beside the workspace and owns the agent, shell, files, Git, LSP,
MCP, and session state. The web UI connects to its existing HTTP/WebSocket API
through SSH, a loopback-only Docker port mapping, or `kubectl port-forward`.
This preserves remote terminal behavior without an ACP filesystem/shell bridge
or a second gateway protocol, and requires only a single copied executable.
The desktop launcher stores SSH workspace profiles and owns bootstrap/download,
port forwarding, readiness, same-origin HTTP/WebSocket proxying, and teardown.

Build on that foundation with:

- Equivalent launcher-managed lifecycle adapters for development containers,
  Kubernetes, and WSL, plus explicit version pinning and upgrade controls.
- Hosted agents that continue running while the client is offline.
- Remote desktop access to agent environments.
- Shareable logs, screenshots, videos, and other run artifacts.
- Web and mobile monitoring of remote sessions.

Benchmarks: [VS Code Remote Development](https://code.visualstudio.com/docs/remote/remote-overview?azure-portal=true) and [Cursor Cloud Agents](https://cursor.com/docs/cloud-agent).

### P2: Event-driven automation and integrations

Local schedules and lifecycle hooks exist. Add:

- GitHub, GitLab, Slack, Linear, Jira, and webhook triggers.
- File-create, file-save, and file-delete hooks.
- Cloud agents triggered by pull requests, issues, incidents, or messages.
- Durable automation history, retries, and notifications.
- Memory and reusable context across automation runs.

Benchmarks: [Kiro Hooks](https://kiro.dev/docs/hooks/) and [Cursor Automations](https://cursor.com/blog/automations).

### P2: Team and enterprise controls

Add:

- Organization policies for models, tools, plugins, and permissions.
- SSO, audit logs, usage reporting, and budgets.
- Centrally managed settings and profiles.
- Settings, keybinding, snippet, task, and extension synchronization.
- Shared instructions and approved integrations.

Benchmark: [VS Code Settings Sync](https://code.visualstudio.com/docs/configure/settings-sync).

## Recommended implementation order

1. AI inline completion and next-edit prediction.
2. Automatic checkpoints with per-hunk accept or reject.
3. Worktree-isolated agent sessions with merge-back.
4. Browser control and screenshot-based UI verification.
5. Structured specifications and task artifacts.
6. Debugger and Test Explorer.

Items 1–3 close the most visible gap with Cursor and VS Code without requiring Wingman to reproduce the entire VS Code extension ecosystem; item 1 is the only one needing a new model integration (a FIM-capable endpoint through the gateway).
