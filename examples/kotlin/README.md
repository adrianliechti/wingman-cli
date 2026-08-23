# Kotlin LSP example

This small Gradle application exercises Wingman's official Kotlin language
server integration. Open this directory as a workspace, wait for Gradle import
and indexing to finish, then try completion, navigation, diagnostics, and hover
on `println`. The first import can take noticeably longer while Gradle resolves
the project; a hover started during that import may only show `Loading...`, so
hover again after indexing has settled.

Wingman downloads the official JetBrains `kotlin-lsp` standalone archive; no
separate Kotlin compiler or editor extension is required. It uses the JBR
bundled in that archive for project import. To build and run the example, use a
local Gradle installation and JDK:

```sh
gradle run
```

Kotlin debugging is deliberately not registered yet. JetBrains' current VS
Code extension exposes only an experimental attach adapter, and its accepted
breakpoints do not reliably stop the JVM.
