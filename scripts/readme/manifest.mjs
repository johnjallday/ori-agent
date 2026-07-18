import { createHash } from 'node:crypto';
import { access, readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const REQUIRED_SCREENSHOT_IDS = ['hero', 'action-center', 'workspace-map', 'workspace'];

const ACCEPTANCE_STATES = new Set(['bootstrap', 'accepted']);
const THEMES = new Set(['dark']);
const SHA256_RE = /^[a-f0-9]{64}$/i;
const GIT_COMMIT_RE = /^[a-f0-9]{7,64}$/i;

function issue(code, message, location = '') {
  return { code, message, location };
}

function isObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function stringValue(value) {
  return typeof value === 'string' ? value.trim() : '';
}

export function isSafeRepositoryPath(value) {
  const candidate = stringValue(value);
  if (!candidate || candidate.includes('\0') || path.isAbsolute(candidate)) return false;
  const normalized = path.posix.normalize(candidate.replaceAll('\\', '/'));
  return normalized !== '.' && normalized !== '..' && !normalized.startsWith('../');
}

export function resolveRepositoryPath(root, relativePath) {
  if (!isSafeRepositoryPath(relativePath)) return null;
  const resolvedRoot = path.resolve(root);
  const resolved = path.resolve(resolvedRoot, relativePath);
  return resolved === resolvedRoot || resolved.startsWith(`${resolvedRoot}${path.sep}`) ? resolved : null;
}

function validateAcceptedField(value, field, location, errors, { allowNull = false } = {}) {
  if (value === null && allowNull) return;
  if (typeof value !== 'string' || !SHA256_RE.test(value)) {
    errors.push(issue('manifest.invalid_checksum', `${field} must be a SHA-256 hex digest${allowNull ? ' or null in bootstrap state' : ''}`, location));
  }
}

export function validateManifest(manifest) {
  const errors = [];
  if (!isObject(manifest)) return [issue('manifest.invalid_root', 'Manifest must be a JSON object.')];

  if (!Number.isInteger(manifest.schema_version) || manifest.schema_version < 1) {
    errors.push(issue('manifest.invalid_schema_version', 'schema_version must be a positive integer.', 'schema_version'));
  }

  const acceptanceState = stringValue(manifest.acceptance_state);
  if (!ACCEPTANCE_STATES.has(acceptanceState)) {
    errors.push(issue('manifest.invalid_acceptance_state', 'acceptance_state must be "bootstrap" or "accepted".', 'acceptance_state'));
  }

  const bootstrap = acceptanceState === 'bootstrap';
  const captureCommit = manifest.last_accepted_capture_commit;
  if (bootstrap ? captureCommit !== null : typeof captureCommit !== 'string' || !GIT_COMMIT_RE.test(captureCommit)) {
    errors.push(issue('manifest.invalid_capture_commit', bootstrap
      ? 'last_accepted_capture_commit must be null in bootstrap state.'
      : 'last_accepted_capture_commit must be a Git commit SHA in accepted state.', 'last_accepted_capture_commit'));
  }

  validateAcceptedField(manifest.accepted_readme_sha256, 'accepted_readme_sha256', 'accepted_readme_sha256', errors, { allowNull: bootstrap });
  if (bootstrap ? manifest.accepted_environment !== null : !isObject(manifest.accepted_environment)) {
    errors.push(issue('manifest.invalid_accepted_environment', bootstrap
      ? 'accepted_environment must be null in bootstrap state.'
      : 'accepted_environment must be an object in accepted state.', 'accepted_environment'));
  }

  if (!Array.isArray(manifest.sensitive_patterns) || manifest.sensitive_patterns.some(pattern => !stringValue(pattern))) {
    errors.push(issue('manifest.invalid_sensitive_patterns', 'sensitive_patterns must be an array of non-empty strings.', 'sensitive_patterns'));
  }

  if (!Array.isArray(manifest.screenshots)) {
    errors.push(issue('manifest.invalid_screenshots', 'screenshots must be an array.', 'screenshots'));
    return errors;
  }

  const ids = new Set();
  const outputs = new Set();
  for (const [index, screenshot] of manifest.screenshots.entries()) {
    const location = `screenshots[${index}]`;
    if (!isObject(screenshot)) {
      errors.push(issue('manifest.invalid_screenshot', 'Each screenshot must be an object.', location));
      continue;
    }

    const id = stringValue(screenshot.id);
    if (!id) {
      errors.push(issue('manifest.missing_id', 'Screenshot id is required.', location));
    } else if (ids.has(id)) {
      errors.push(issue('manifest.duplicate_id', `Screenshot id "${id}" is duplicated.`, `${location}.id`));
    } else {
      ids.add(id);
    }

    const outputPath = stringValue(screenshot.output_path);
    if (!isSafeRepositoryPath(outputPath) || !outputPath.endsWith('.webp')) {
      errors.push(issue('manifest.invalid_output_path', 'output_path must be a safe repository-relative .webp path.', `${location}.output_path`));
    } else if (outputs.has(outputPath)) {
      errors.push(issue('manifest.duplicate_output_path', `output_path "${outputPath}" is duplicated.`, `${location}.output_path`));
    } else {
      outputs.add(outputPath);
    }

    if (!stringValue(screenshot.route).startsWith('/')) {
      errors.push(issue('manifest.invalid_route', 'route must be an absolute application path starting with /.', `${location}.route`));
    }
    if (!stringValue(screenshot.scenario_id)) {
      errors.push(issue('manifest.missing_scenario', 'scenario_id is required.', `${location}.scenario_id`));
    }
    if (!isObject(screenshot.viewport) || !Number.isInteger(screenshot.viewport.width) || !Number.isInteger(screenshot.viewport.height) || screenshot.viewport.width <= 0 || screenshot.viewport.height <= 0) {
      errors.push(issue('manifest.invalid_viewport', 'viewport must contain positive integer width and height.', `${location}.viewport`));
    }
    if (screenshot.device_scale_factor !== 2) {
      errors.push(issue('manifest.invalid_device_scale_factor', 'device_scale_factor must be 2 for the README contract.', `${location}.device_scale_factor`));
    }
    if (!THEMES.has(screenshot.theme)) {
      errors.push(issue('manifest.invalid_theme', 'theme must be dark for the V1 portfolio.', `${location}.theme`));
    }
    if (screenshot.locale !== 'en-US' || screenshot.timezone !== 'UTC') {
      errors.push(issue('manifest.invalid_locale_or_timezone', 'locale must be en-US and timezone must be UTC.', location));
    }
    if (!Array.isArray(screenshot.required_visible_selectors) || screenshot.required_visible_selectors.length === 0 || screenshot.required_visible_selectors.some(selector => !stringValue(selector))) {
      errors.push(issue('manifest.invalid_required_selectors', 'required_visible_selectors must contain at least one selector.', `${location}.required_visible_selectors`));
    }
    if (!stringValue(screenshot.caption) || !stringValue(screenshot.alt_text)) {
      errors.push(issue('manifest.missing_copy', 'caption and alt_text are required.', location));
    }
    if (![420, 820].includes(screenshot.display_width)) {
      errors.push(issue('manifest.invalid_display_width', 'display_width must be 420 or 820.', `${location}.display_width`));
    }
    if (!Array.isArray(screenshot.owner_paths) || screenshot.owner_paths.length === 0 || screenshot.owner_paths.some(ownerPath => !isSafeRepositoryPath(ownerPath))) {
      errors.push(issue('manifest.invalid_owner_paths', 'owner_paths must contain safe repository-relative paths.', `${location}.owner_paths`));
    }
    validateAcceptedField(screenshot.accepted_sha256, 'accepted_sha256', `${location}.accepted_sha256`, errors, { allowNull: bootstrap });
    if (bootstrap ? screenshot.accepted_bytes !== null : !Number.isInteger(screenshot.accepted_bytes) || screenshot.accepted_bytes <= 0) {
      errors.push(issue('manifest.invalid_accepted_bytes', bootstrap
        ? 'accepted_bytes must be null in bootstrap state.'
        : 'accepted_bytes must be a positive integer in accepted state.', `${location}.accepted_bytes`));
    }
  }

  const expected = new Set(REQUIRED_SCREENSHOT_IDS);
  for (const id of REQUIRED_SCREENSHOT_IDS) {
    if (!ids.has(id)) errors.push(issue('manifest.missing_required_id', `Missing required screenshot id "${id}".`, 'screenshots'));
  }
  for (const id of ids) {
    if (!expected.has(id)) errors.push(issue('manifest.unexpected_id', `Unexpected screenshot id "${id}".`, 'screenshots'));
  }
  if (manifest.screenshots.length !== REQUIRED_SCREENSHOT_IDS.length) {
    errors.push(issue('manifest.invalid_portfolio_size', `screenshots must contain exactly ${REQUIRED_SCREENSHOT_IDS.length} entries.`, 'screenshots'));
  }
  return errors;
}

export async function loadManifest(root, manifestPath = 'docs/readme-screenshots.json') {
  const resolved = resolveRepositoryPath(root, manifestPath);
  if (!resolved) throw new Error(`Unsafe manifest path: ${manifestPath}`);
  const content = await readFile(resolved, 'utf8');
  return { manifest: JSON.parse(content), path: resolved };
}

function cleanReferenceTarget(rawTarget) {
  const trimmed = stringValue(rawTarget).replace(/^<|>$/g, '');
  return trimmed.split(/[?#]/, 1)[0];
}

function isExternalReference(target) {
  return /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(target) || target.startsWith('#');
}

export function extractReadmeLocalReferences(markdown) {
  const references = [];
  const addReference = (kind, rawTarget, source) => {
    const target = cleanReferenceTarget(rawTarget);
    if (target && !isExternalReference(target)) references.push({ kind, target, source });
  };

  for (const match of markdown.matchAll(/(!?)\[[^\]]*\]\((<[^>]+>|[^\s)]+)(?:\s+[^)]*)?\)/g)) {
    addReference(match[1] === '!' ? 'image' : 'link', match[2], 'markdown');
  }
  for (const match of markdown.matchAll(/<img\b[^>]*\bsrc\s*=\s*(["'])(.*?)\1[^>]*>/gi)) {
    addReference('image', match[2], 'html');
  }
  for (const match of markdown.matchAll(/<a\b[^>]*\bhref\s*=\s*(["'])(.*?)\1[^>]*>/gi)) {
    addReference('link', match[2], 'html');
  }
  return references;
}

async function pathExists(filePath) {
  try {
    await access(filePath);
    return true;
  } catch {
    return false;
  }
}

export async function validateReadmeContent(root, manifest, markdown, { virtualExistingPaths = [] } = {}) {
  const errors = [];
  if (typeof markdown !== 'string') {
    return [issue('readme.invalid_content', 'README content must be a string.', 'README.md')];
  }
  const references = extractReadmeLocalReferences(markdown);
  const manifestOutputs = new Set((manifest.screenshots || []).map(screenshot => screenshot.output_path));
  const virtualPaths = new Set(virtualExistingPaths);
  const productImageReferences = new Set();
  for (const reference of references) {
    if (reference.target.includes('test-results/readme-refresh/')) {
      errors.push(issue('readme.staging_reference', `README must not reference staging output "${reference.target}".`, reference.target));
    }
    const resolved = resolveRepositoryPath(root, reference.target);
    if (!resolved) {
      errors.push(issue('readme.unsafe_reference', `README reference "${reference.target}" escapes the repository.`, reference.target));
      continue;
    }
    if (!virtualPaths.has(reference.target) && !await pathExists(resolved)) {
      errors.push(issue('readme.missing_reference', `README reference "${reference.target}" does not exist.`, reference.target));
    }
    if (reference.kind === 'image' && reference.target.startsWith('docs/images/')) {
      productImageReferences.add(reference.target);
      if (!manifestOutputs.has(reference.target)) {
        errors.push(issue('readme.unmanifested_product_image', `README product image "${reference.target}" is not in the screenshot manifest.`, reference.target));
      }
    }
  }
  for (const outputPath of manifestOutputs) {
    if (!productImageReferences.has(outputPath)) {
      errors.push(issue('readme.missing_manifest_image', `Manifest output "${outputPath}" is not referenced by README.`, outputPath));
    }
  }
  return errors;
}

export async function validateReadmeReferences(root, manifest, readmePath = 'README.md') {
  const resolvedReadme = resolveRepositoryPath(root, readmePath);
  if (!resolvedReadme) return [issue('readme.invalid_path', 'README path must be safe and repository-relative.', readmePath)];
  let markdown;
  try {
    markdown = await readFile(resolvedReadme, 'utf8');
  } catch (error) {
    return [issue('readme.unreadable', `Could not read ${readmePath}: ${error.message}`, readmePath)];
  }
  return validateReadmeContent(root, manifest, markdown);
}

export async function sha256(filePath) {
  return createHash('sha256').update(await readFile(filePath)).digest('hex');
}

function readUInt24LE(buffer, offset) {
  return buffer[offset] | (buffer[offset + 1] << 8) | (buffer[offset + 2] << 16);
}

function parseWebP(buffer) {
  if (buffer.length < 20 || buffer.toString('ascii', 0, 4) !== 'RIFF' || buffer.toString('ascii', 8, 12) !== 'WEBP') {
    throw new Error('not a WebP RIFF file');
  }
  let offset = 12;
  while (offset + 8 <= buffer.length) {
    const chunk = buffer.toString('ascii', offset, offset + 4);
    const length = buffer.readUInt32LE(offset + 4);
    const dataOffset = offset + 8;
    if (dataOffset + length > buffer.length) throw new Error(`truncated ${chunk} WebP chunk`);
    if (chunk === 'VP8X' && length >= 10) {
      return {
        format: 'webp',
        width: readUInt24LE(buffer, dataOffset + 4) + 1,
        height: readUInt24LE(buffer, dataOffset + 7) + 1
      };
    }
    if (chunk === 'VP8L' && length >= 5 && buffer[dataOffset] === 0x2f) {
      const bits = buffer.readUInt32LE(dataOffset + 1);
      return { format: 'webp', width: 1 + (bits & 0x3fff), height: 1 + ((bits >>> 14) & 0x3fff) };
    }
    if (chunk === 'VP8 ' && length >= 10 && buffer[dataOffset + 3] === 0x9d && buffer[dataOffset + 4] === 0x01 && buffer[dataOffset + 5] === 0x2a) {
      return {
        format: 'webp',
        width: buffer.readUInt16LE(dataOffset + 6) & 0x3fff,
        height: buffer.readUInt16LE(dataOffset + 8) & 0x3fff
      };
    }
    offset = dataOffset + length + (length % 2);
  }
  throw new Error('WebP dimensions chunk not found');
}

export async function readImageMetadata(filePath) {
  const buffer = await readFile(filePath);
  if (buffer.length >= 24 && buffer.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))) {
    return { format: 'png', width: buffer.readUInt32BE(16), height: buffer.readUInt32BE(20) };
  }
  return parseWebP(buffer);
}

export async function validateImageContract(root, manifest) {
  const errors = [];
  let totalBytes = 0;
  for (const [index, screenshot] of (manifest.screenshots || []).entries()) {
    const location = `screenshots[${index}]`;
    const filePath = resolveRepositoryPath(root, screenshot.output_path);
    if (!filePath || !await pathExists(filePath)) {
      errors.push(issue('image.missing_output', `Screenshot output "${screenshot.output_path}" does not exist.`, `${location}.output_path`));
      continue;
    }
    let image;
    let fileStat;
    try {
      [image, fileStat] = await Promise.all([readImageMetadata(filePath), stat(filePath)]);
    } catch (error) {
      errors.push(issue('image.unreadable', `Could not inspect "${screenshot.output_path}": ${error.message}`, `${location}.output_path`));
      continue;
    }
    totalBytes += fileStat.size;
    if (image.format !== 'webp') {
      errors.push(issue('image.invalid_format', `Screenshot output "${screenshot.output_path}" must be WebP.`, `${location}.output_path`));
    }
    const expectedWidth = screenshot.viewport?.width * screenshot.device_scale_factor;
    const expectedHeight = screenshot.viewport?.height * screenshot.device_scale_factor;
    if (image.width !== expectedWidth || image.height !== expectedHeight) {
      errors.push(issue('image.invalid_dimensions', `Screenshot output "${screenshot.output_path}" is ${image.width}x${image.height}; expected ${expectedWidth}x${expectedHeight}.`, `${location}.output_path`));
    }
    if (fileStat.size > 750 * 1024) {
      errors.push(issue('image.file_too_large', `Screenshot output "${screenshot.output_path}" exceeds 750 KB.`, `${location}.output_path`));
    }
    if (manifest.acceptance_state === 'accepted') {
      if (fileStat.size !== screenshot.accepted_bytes) {
        errors.push(issue('image.accepted_size_mismatch', `Screenshot output "${screenshot.output_path}" does not match accepted byte count.`, `${location}.accepted_bytes`));
      }
      if (await sha256(filePath) !== screenshot.accepted_sha256) {
        errors.push(issue('image.accepted_checksum_mismatch', `Screenshot output "${screenshot.output_path}" does not match accepted checksum.`, `${location}.accepted_sha256`));
      }
    }
  }
  if (totalBytes > Math.floor(2.5 * 1024 * 1024)) {
    errors.push(issue('image.portfolio_too_large', 'The README screenshot portfolio exceeds 2.5 MB.', 'screenshots'));
  }
  return errors;
}

export async function validateRepository(root) {
  const errors = [];
  let manifest;
  try {
    ({ manifest } = await loadManifest(root));
  } catch (error) {
    return { root: path.resolve(root), manifest: null, errors: [issue('manifest.unreadable', error.message, 'docs/readme-screenshots.json')] };
  }
  errors.push(...validateManifest(manifest));
  if (errors.some(error => error.code.startsWith('manifest.'))) {
    return { root: path.resolve(root), manifest, errors };
  }
  errors.push(...await validateReadmeReferences(root, manifest));
  errors.push(...await validateImageContract(root, manifest));
  if (manifest.acceptance_state === 'accepted') {
    const readmePath = resolveRepositoryPath(root, 'README.md');
    if (readmePath && await pathExists(readmePath) && await sha256(readmePath) !== manifest.accepted_readme_sha256) {
      errors.push(issue('readme.accepted_checksum_mismatch', 'README.md does not match its accepted checksum.', 'accepted_readme_sha256'));
    }
  }
  return { root: path.resolve(root), manifest, errors };
}

export function formatReport(report) {
  if (report.errors.length === 0) return `README contract passed for ${report.root}.`;
  const lines = [`README contract failed with ${report.errors.length} issue(s):`];
  for (const entry of report.errors) {
    lines.push(`- [${entry.code}]${entry.location ? ` ${entry.location}:` : ''} ${entry.message}`);
  }
  return lines.join('\n');
}

async function runCLI() {
  const args = process.argv.slice(2);
  const rootFlag = args.indexOf('--root');
  const root = rootFlag >= 0 && args[rootFlag + 1] ? args[rootFlag + 1] : process.cwd();
  const report = await validateRepository(root);
  if (args.includes('--json')) {
    console.log(JSON.stringify(report, null, 2));
  } else {
    console.log(formatReport(report));
  }
  if (report.errors.length > 0) process.exitCode = 1;
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  runCLI().catch(error => {
    console.error(`README contract failed: ${error.message}`);
    process.exitCode = 1;
  });
}

export const manifestModulePath = fileURLToPath(import.meta.url);
