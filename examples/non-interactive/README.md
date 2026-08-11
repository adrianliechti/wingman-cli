# Non-interactive structured output

Run the example from any repository you want Wingman to inspect:

```sh
examples/non-interactive/run.sh
```

It runs unattended, prints the final JSON to stdout, and saves the same value
as `project.json` beside the example with `tee`. The command fails if the final
response is not valid JSON or does not match `project.schema.json`.

For schema-less structured output, use `--json` by itself:

```sh
wingman exec --json "Summarize this repository" | jq
```
