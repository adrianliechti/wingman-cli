# Debugger samples

These deliberately small projects exercise Wingman's runnable-target detection
and launch profiles. Open this directory (or an individual child) as a Wingman
workspace, update managed editor tools, then use the inline **Run | Debug**
action over the entry point. Every sample has a managed language server and
debug adapter.

| Directory | Runtime | Adapter | Installation |
|-----------|---------|---------|--------------|
| `go` | Go | Delve (`dlv`) | Managed with `go install` |
| `python` | Python | debugpy (`debugpy-adapter`) | Managed with `pip` |
| `java` | Java/Maven | JDT LS plus Microsoft java-debug | Managed with Maven |
| `typescript` | TypeScript through project-local `tsx` | vscode-js-debug | Managed from the official GitHub release, best effort |
| `react-vite` | React in a Vite browser session | vscode-js-debug | Same managed JavaScript adapter |
| `rust` | Rust/Cargo | CodeLLDB (`codelldb`) | Managed from the official GitHub release, best effort |
| `dotnet` | .NET/C# | NetCoreDbg (`netcoredbg`) | Managed from the official GitHub release, best effort |

The React sample's browser profile expects `npm install && npm run dev` to be
running first. The Rust and .NET profiles expect `cargo build` and `dotnet build`
to have produced their debug executables. The TypeScript sample expects
`npm install`. No generated output, lockfiles, or installed dependencies are
checked in.
