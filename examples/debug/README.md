# Debugger samples

These deliberately small projects exercise Wingman's runnable-target detection
and launch profiles. Open this directory (or an individual child) as a Wingman
workspace, install the matching adapter, then use the inline **Run | Debug**
action over the entry point.

| Directory | Runtime | Adapter |
|-----------|---------|---------|
| `go` | Go | Delve (`dlv`) |
| `python` | Python | debugpy (`debugpy-adapter`) |
| `java` | Java/Maven | JDT LS plus the Microsoft java-debug bundle |
| `rust` | Rust/Cargo | CodeLLDB (`codelldb`) |
| `dotnet` | .NET/C# | NetCoreDbg (`netcoredbg`) |
| `typescript` | TypeScript on modern Node.js | vscode-js-debug |
| `react-vite` | React in a Vite browser session | vscode-js-debug |

The React sample's browser profile expects `npm install && npm run dev` to be
running first. The .NET profile expects `dotnet build` to have produced the DLL.
No generated output, lockfiles, or installed dependencies are checked in.
