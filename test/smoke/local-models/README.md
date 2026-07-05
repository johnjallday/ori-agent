# Local structured-output smoke / metrics harness (WS9.36)

Measures **first-pass schema-valid rate** and wall time for structured task
output on local models — the evidence for **Success Metric 2** (≥ 90% first-pass
schema-valid with constrained decoding).

This is a **manual** harness. It is compiled by `go build ./...` but never run in
CI, because it needs a running Ollama with the models pulled.

## Prerequisites

```bash
ollama serve                       # or the desktop app
ollama pull llama3.1:8b
ollama pull qwen2.5:7b-instruct
```

## Capture the baseline BEFORE trusting WS3

Run once with the schema disabled (prompt-only, the pre-WS3 behavior) and record
the number:

```bash
go run ./test/smoke/local-models -baseline
```

## Measure constrained decoding (WS3)

```bash
go run ./test/smoke/local-models
```

Compare the "Overall first-pass schema-valid" line against the baseline. WS3 is
working when the constrained run is meaningfully higher and clears 90%.

## Options

```bash
go run ./test/smoke/local-models \
  -models llama3.1:8b,qwen2.5:7b-instruct \
  -runs 3

OLLAMA_BASE_URL=http://localhost:11434 go run ./test/smoke/local-models
```

- `-baseline` — omit the JSON schema (prompt-only) to capture the pre-WS3 rate.
- `-models`   — comma-separated Ollama model tags (default `llama3.1:8b,qwen2.5:7b-instruct`).
- `-runs`     — runs per fixture; validity is averaged (default 1). Use 3+ to
  smooth out sampling noise.

## Fixtures

Three output-spec tasks live in `main.go`: `extract_weather`,
`classify_sentiment`, `pick_priority_task`. Each declares a JSON schema and the
required keys/kinds an answer must contain. Add fixtures by appending to
`fixtures()`.

## Recording a baseline

Paste the two "Overall first-pass schema-valid" lines (baseline vs constrained)
into the PR that lands WS3, with the model tags and Ollama version, so the metric
is reproducible.
