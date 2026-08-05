const DEFAULT_HISTORY_COLUMNS = [
  'executed_at',
  'status',
  'validation_status',
  'storage_status',
  'run_id',
  'duration_ms'
];

function cleanColumnName(value, fallback = 'value') {
  const cleaned = String(value ?? '')
    .replace(/\s+/g, ' ')
    .trim();
  return cleaned || fallback;
}

function normalizeCellValue(value) {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch (_error) {
    return String(value);
  }
}

function normalizeRows(rows) {
  return (Array.isArray(rows) ? rows : [])
    .map(row => {
      if (!row || typeof row !== 'object' || Array.isArray(row)) return null;
      const normalized = {};
      Object.entries(row).forEach(([key, value]) => {
        normalized[cleanColumnName(key)] = normalizeCellValue(value);
      });
      return normalized;
    })
    .filter(Boolean);
}

function uniqueColumns(rows, preferredColumns = []) {
  const columns = [];
  const seen = new Set();
  const add = value => {
    const column = cleanColumnName(value, '');
    const key = column.toLowerCase();
    if (!column || seen.has(key)) return;
    seen.add(key);
    columns.push(column);
  };

  preferredColumns.forEach(add);
  rows.forEach(row => Object.keys(row || {}).forEach(add));
  return columns;
}

function hasTabularShape(columns, rows) {
  return Array.isArray(columns) && columns.length > 0 && Array.isArray(rows) && rows.length > 0;
}

function makeArtifact({ kind, title, source, columns, rows }) {
  const normalizedRows = normalizeRows(rows);
  const normalizedColumns = uniqueColumns(normalizedRows, columns);
  if (!hasTabularShape(normalizedColumns, normalizedRows)) return null;
  return {
    kind,
    title: title || 'CSV-ready result',
    source: source || 'result',
    columns: normalizedColumns,
    rows: normalizedRows,
    csv: rowsToCSV(normalizedColumns, normalizedRows)
  };
}

export function rowsToCSV(columns, rows) {
  const activeColumns = uniqueColumns(rows, columns);
  const escapeValue = value => {
    const cell = normalizeCellValue(value);
    if (/[",\r\n]/.test(cell)) {
      return `"${cell.replace(/"/g, '""')}"`;
    }
    return cell;
  };

  return [
    activeColumns.map(escapeValue).join(','),
    ...rows.map(row => activeColumns.map(column => escapeValue(row?.[column] ?? '')).join(','))
  ].join('\n');
}

export function parseDelimitedRecords(text, delimiter = ',') {
  const input = String(text ?? '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n');
  const rows = [];
  let row = [];
  let field = '';
  let inQuotes = false;

  for (let index = 0; index < input.length; index += 1) {
    const char = input[index];

    if (inQuotes) {
      if (char === '"') {
        if (input[index + 1] === '"') {
          field += '"';
          index += 1;
        } else {
          inQuotes = false;
        }
      } else {
        field += char;
      }
      continue;
    }

    if (char === '"') {
      inQuotes = true;
      continue;
    }
    if (char === delimiter) {
      row.push(field);
      field = '';
      continue;
    }
    if (char === '\n') {
      row.push(field);
      rows.push(row);
      row = [];
      field = '';
      continue;
    }
    field += char;
  }

  row.push(field);
  rows.push(row);

  return rows
    .map(items => items.map(item => String(item ?? '').trim()))
    .filter((items, index, allRows) => {
      const nonEmpty = items.some(item => item !== '');
      if (nonEmpty) return true;
      return index < allRows.length - 1;
    });
}

function rowsFromDelimitedRecords(records) {
  if (!Array.isArray(records) || records.length < 2) return null;
  const header = records[0].map((value, index) => cleanColumnName(value, `column_${index + 1}`));
  if (header.length < 2) return null;
  const rows = records
    .slice(1)
    .filter(record => record.some(value => String(value || '').trim()))
    .map(record => {
      const row = {};
      header.forEach((column, index) => {
        row[column] = record[index] ?? '';
      });
      return row;
    });
  if (rows.length === 0) return null;
  return makeArtifact({
    kind: 'csv',
    title: 'CSV result',
    source: 'csv',
    columns: header,
    rows
  });
}

function parseDelimitedTable(text, delimiter) {
  const trimmed = String(text ?? '').trim();
  if (!trimmed) return null;

  const records = parseDelimitedRecords(trimmed, delimiter);
  if (records.length < 2) return null;
  if (records[0].length < 2 || records[1].length < 2) return null;
  return rowsFromDelimitedRecords(records);
}

function parseFencedCSV(text) {
  const match = String(text ?? '').match(/```(?:csv|tsv)\s*\n([\s\S]*?)```/i);
  if (!match) return null;
  const fence = match[0].toLowerCase();
  const delimiter = fence.startsWith('```tsv') ? '\t' : ',';
  const artifact = parseDelimitedTable(match[1], delimiter);
  if (!artifact) return null;
  return {
    ...artifact,
    kind: 'fenced_csv',
    title: 'CSV block',
    source: 'fenced_csv'
  };
}

function splitMarkdownTableRow(line) {
  let value = String(line ?? '').trim();
  if (value.startsWith('|')) value = value.slice(1);
  if (value.endsWith('|')) value = value.slice(0, -1);
  return value.split('|').map(cell => cell.trim());
}

function isMarkdownSeparatorRow(line) {
  const cells = splitMarkdownTableRow(line);
  return cells.length > 1 && cells.every(cell => /^:?-{3,}:?$/.test(cell.replace(/\s+/g, '')));
}

function parseMarkdownTable(text) {
  const lines = String(text ?? '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
    .split('\n');
  for (let index = 0; index < lines.length - 1; index += 1) {
    if (!lines[index].includes('|') || !isMarkdownSeparatorRow(lines[index + 1])) continue;
    const header = splitMarkdownTableRow(lines[index]).map((value, columnIndex) =>
      cleanColumnName(value, `column_${columnIndex + 1}`)
    );
    if (header.length < 2) continue;

    const rows = [];
    for (let rowIndex = index + 2; rowIndex < lines.length; rowIndex += 1) {
      const line = lines[rowIndex];
      if (!line.includes('|') || !String(line).trim()) break;
      const values = splitMarkdownTableRow(line);
      if (values.length < 2) break;
      const row = {};
      header.forEach((column, columnIndex) => {
        row[column] = values[columnIndex] ?? '';
      });
      rows.push(row);
    }

    if (rows.length > 0) {
      return makeArtifact({
        kind: 'markdown_table',
        title: 'Markdown table',
        source: 'markdown_table',
        columns: header,
        rows
      });
    }
  }
  return null;
}

function tryParseJSON(text) {
  const trimmed = String(text ?? '').trim();
  if (!trimmed || !/^[{[]/.test(trimmed)) return null;
  try {
    return JSON.parse(trimmed);
  } catch (_error) {
    return null;
  }
}

function rowsFromObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const row = {};
  Object.entries(value).forEach(([key, cell]) => {
    row[cleanColumnName(key)] = normalizeCellValue(cell);
  });
  return [row];
}

function tableFromJSONValue(value, title = 'Structured result', source = 'json') {
  if (Array.isArray(value)) {
    const objectRows = value.filter(
      item => item && typeof item === 'object' && !Array.isArray(item)
    );
    if (objectRows.length !== value.length || objectRows.length === 0) return null;
    return makeArtifact({
      kind: 'json',
      title,
      source,
      columns: uniqueColumns(objectRows),
      rows: objectRows
    });
  }

  if (!value || typeof value !== 'object') return null;

  const displayType = String(value.displayType || value.display_type || '').toLowerCase();
  if (displayType === 'table' && value.data !== undefined) {
    return tableFromJSONValue(value.data, value.title || title, 'structured_result');
  }
  if (Array.isArray(value.data) || (value.data && typeof value.data === 'object')) {
    const fromData = tableFromJSONValue(value.data, value.title || title, 'structured_result');
    if (fromData) return fromData;
  }
  if (Array.isArray(value.rows)) {
    return tableFromJSONValue(value.rows, value.title || title, source);
  }

  for (const [key, nested] of Object.entries(value)) {
    if (
      Array.isArray(nested) &&
      nested.some(item => item && typeof item === 'object' && !Array.isArray(item))
    ) {
      return tableFromJSONValue(nested, cleanColumnName(key), source);
    }
  }

  return makeArtifact({
    kind: 'json',
    title,
    source,
    columns: Object.keys(value),
    rows: rowsFromObject(value)
  });
}

function parseJSONTable(text) {
  const parsed = tryParseJSON(text);
  if (parsed === null) return null;
  return tableFromJSONValue(parsed, 'Structured result', 'json');
}

function structuredOutputArtifact(task) {
  const output = task?.context?.structured_output;
  if (!output || typeof output !== 'object') return null;
  const artifact = tableFromJSONValue(output, 'Structured output', 'output_schema');
  if (!artifact) return null;
  return {
    ...artifact,
    kind: 'output_schema',
    source: 'output_schema',
    title: 'Structured output'
  };
}

function parsePlainDelimitedTable(text) {
  const trimmed = String(text ?? '').trim();
  if (!trimmed) return null;

  const lines = trimmed.split(/\r?\n/).filter(line => line.trim());
  if (lines.length < 2) return null;

  const first = lines[0];
  const commaCount = (first.match(/,/g) || []).length;
  const tabCount = (first.match(/\t/g) || []).length;
  const delimiter = tabCount > commaCount ? '\t' : ',';
  if ((delimiter === ',' && commaCount < 1) || (delimiter === '\t' && tabCount < 1)) return null;

  const artifact = parseDelimitedTable(trimmed, delimiter);
  if (!artifact) return null;
  return {
    ...artifact,
    kind: delimiter === '\t' ? 'tsv' : 'csv',
    source: delimiter === '\t' ? 'tsv' : 'csv',
    title: delimiter === '\t' ? 'TSV result' : 'CSV result'
  };
}

export function detectTabularResult(text, task = null) {
  return (
    structuredOutputArtifact(task) ||
    parseJSONTable(text) ||
    parseFencedCSV(text) ||
    parseMarkdownTable(text) ||
    parsePlainDelimitedTable(text)
  );
}

function summarizeText(value, maxLength = 420) {
  const normalized = String(value ?? '')
    .replace(/\s+/g, ' ')
    .trim();
  if (!normalized || normalized.length <= maxLength) return normalized;
  return `${normalized.slice(0, maxLength - 1).trim()}...`;
}

function buildHistoryRow(entry, parsed, parsedRow = null, rowIndex = 0) {
  const row = {
    executed_at: String(entry?.executed_at || ''),
    status: String(entry?.status || '')
  };
  if (entry?.run_id) row.run_id = String(entry.run_id);
  const validation = entry?.validation_result || entry?.validation || null;
  if (validation?.validation_status) row.validation_status = String(validation.validation_status);
  if (validation?.storage_status) row.storage_status = String(validation.storage_status);
  if (entry?.duration !== undefined && entry?.duration !== null && entry.duration !== '') {
    row.duration_ms = String(entry.duration);
  }
  if (parsed && parsedRow) {
    if (parsed.rows.length > 1) row.result_row = String(rowIndex + 1);
    Object.entries(parsedRow).forEach(([key, value]) => {
      row[key] = normalizeCellValue(value);
    });
  } else {
    row.summary = summarizeText(entry?.summary || entry?.result || entry?.error || '');
  }
  return row;
}

export function buildRunHistoryArtifact(task) {
  const history = Array.isArray(task?.execution_history) ? task.execution_history : [];
  if (history.length < 2) return null;

  const rows = [];
  const parsedColumns = [];
  let parsedCount = 0;
  let hasSummary = false;

  history.forEach(entry => {
    const validation = entry?.validation_result || entry?.validation || null;
    const normalizedRow = validation?.normalized_row;
    if (normalizedRow && typeof normalizedRow === 'object' && !Array.isArray(normalizedRow)) {
      parsedCount += 1;
      Object.keys(normalizedRow).forEach(column => parsedColumns.push(column));
      rows.push(buildHistoryRow(entry, { rows: [normalizedRow] }, normalizedRow, 0));
      return;
    }
    const raw = String(entry?.result || entry?.summary || '').trim();
    const parsed = detectTabularResult(raw);
    if (parsed && parsed.rows.length > 0) {
      parsedCount += 1;
      parsed.columns.forEach(column => parsedColumns.push(column));
      parsed.rows.forEach((parsedRow, rowIndex) => {
        rows.push(buildHistoryRow(entry, parsed, parsedRow, rowIndex));
      });
      return;
    }
    hasSummary = true;
    rows.push(buildHistoryRow(entry, null));
  });

  const preferred = DEFAULT_HISTORY_COLUMNS.filter(column => rows.some(row => row[column]));
  if (parsedCount > 0) preferred.push(...parsedColumns);
  if (rows.some(row => row.result_row)) preferred.push('result_row');
  if (hasSummary) preferred.push('summary');

  return makeArtifact({
    kind: parsedCount > 0 ? 'run_history_table' : 'run_history_summary',
    title: parsedCount > 0 ? 'Run history dataset' : 'Run history summary',
    source: 'run_history',
    columns: preferred,
    rows
  });
}

export function buildTaskResultArtifact(task) {
  const historyArtifact = buildRunHistoryArtifact(task);
  // Only let the accumulated history take over the "Latest result" view when
  // runs actually produced a tabular dataset (run_history_table). A
  // summary-only history (run_history_summary) is just execution metadata and
  // duplicates the "Recent runs" list, so fall through to the latest run's own
  // result instead.
  if (historyArtifact && historyArtifact.kind === 'run_history_table') return historyArtifact;
  return detectTabularResult(task?.result || '', task);
}

export function artifactToCSVFence(artifact) {
  if (!artifact || !artifact.csv) return '';
  return ['```csv', artifact.csv, '```'].join('\n');
}
