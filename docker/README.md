# Worker image stacks

Each subdirectory defines one Wingman worker stack. Docker builds use the
repository root as their context so every stack can compile the same tagged
Wingman source checkout.

| Stack | Image tag | Architectures | Purpose |
| --- | --- | --- | --- |
| `slim` | `wingman-agent:slim` | amd64, arm64 | Minimal Debian worker and the runtime default |

Future stacks such as `go`, `java`, and `python` should follow the same
`docker/<stack>/Dockerfile` layout, use the stack name as the image tag, and
declare comma-separated target architectures in `docker/<stack>/platforms`.
`task image:build` and `task image:publish` discover and iterate every stack
subdirectory automatically.

The images do not create an account or select a runtime identity. Docker and
Kubernetes configure the worker UID/GID when they create an instance.
