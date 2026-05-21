# Task Output Contracts

Task output contracts make recurring task storage behave like a small data pipeline. When a task appends each run to CSV, the task can define the exact row shape that must be present before Ori writes to the file.

## Contract Format

Contracts are stored on the task as `output_contract`. The `source` is usually `manual`, `ai_suggested`, or `csv_header`.

```json
{
  "source": "manual",
  "columns": [
    {
      "name": "date",
      "type": "date",
      "required": true,
      "description": "Observation date"
    },
    {
      "name": "pollen_count",
      "type": "number",
      "required": true,
      "description": "Reported pollen level"
    }
  ]
}
```

Supported column types are `string`, `number`, `boolean`, and `date`.

## Execution Behavior

When a task has an output contract, Ori adds final-answer instructions asking the agent to return one JSON object that matches the contract. After the run completes, Ori validates the result before automatic storage.

Valid contracted results are converted to contract-ordered CSV. For append mode, only the CSV rows matching the contract are written.

Invalid contracted results are not written. The execution history entry is marked with:

- `validation_status: needs_review`
- `storage_status: skipped_invalid`
- structured validation errors
- a raw output reference for review

Tasks without an output contract keep the existing storage behavior. Their storage metadata is marked `not_applicable` when automatic storage runs.

## UI Surfaces

The task create/edit modal shows an **Output Contract** section when **Append to CSV** is selected. Users can add, remove, rename, type, and describe columns. The modal requests an AI suggestion from `/api/orchestration/tasks/output-contract/suggest`, caches the suggestion for the current draft, and falls back to manual editing if suggestion fails.

The task details page summarizes the result storage mode and contract columns. Run history labels contracted storage outcomes as `Saved`, `Needs Review`, `Dismissed`, or `Manually Approved`.

When a run is held for review, the task details page shows the latest invalid result with validation errors. Users can copy the raw output, re-run the task, dismiss the review, or edit the row in a table/raw CSV editor and approve the append. Manual approval reuses the same validator before writing to CSV.

If the task was executed through Workspace Runs, Ori mirrors the validation and storage status onto the workspace-run record as `task_output`. This stores contract status, storage status, contract version, and validation errors, but not the raw task output.

## Existing CSV Files

When an append-to-CSV task points at an existing file and has no contract, Ori can bootstrap a contract from only the first header row. Header-derived columns default to `string`, are marked required, and never rewrite or backfill the CSV file.

## Current Limits

This release validates structure only. It does not check factual accuracy, numeric ranges, regex patterns, enums, or duplicate rows.
