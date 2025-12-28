#!/usr/bin/env python3
"""
Fix malformed logger patterns where comments got embedded between logger. and Error().

Before:
    if respErr := orihttp.RespondBadRequest(w, "message"); respErr != nil {
        logger.

            // Some comment
            Error("Failed to write response", logger.Fields{"error": respErr})
    }

After:
    orihttp.BadRequest(w, "message")
    // Some comment
"""

import re
import sys
from pathlib import Path


def fix_file(filepath: Path) -> tuple[int, bool]:
    """Fix a single file. Returns (count of changes, whether file was modified)."""
    content = filepath.read_text()
    original = content

    count = 0

    # Mapping from old function names to new ones
    migrations = {
        'RespondBadRequest': 'BadRequest',
        'RespondNotFound': 'NotFound',
        'RespondInternalError': 'InternalError',
        'RespondMethodNotAllowed': 'MethodNotAllowed',
        'RespondUnauthorized': 'Unauthorized',
        'RespondForbidden': 'Forbidden',
        'RespondConflict': 'Conflict',
        'RespondServiceUnavailable': 'ServiceUnavailable',
        'RespondNotImplemented': 'NotImplemented',
    }

    for old_func, new_func in migrations.items():
        if old_func == 'RespondMethodNotAllowed':
            # No message parameter - malformed pattern
            pattern = (
                r'if (?:respErr|err) := orihttp\.' + old_func + r'\(w\); (?:respErr|err) != nil \{\s*'
                r'logger\.\s*'
                r'((?:\s*//[^\n]*\n)+)\s*'  # Capture comments
                r'Error\("Failed to write response", logger\.Fields\{"error": (?:respErr|err)\}\)\s*'
                r'\}'
            )
            def make_replacement_no_msg(match, new_func=new_func):
                comments = match.group(1).strip()
                # Dedent comments
                lines = comments.split('\n')
                dedented = '\n'.join(line.strip() for line in lines if line.strip())
                return f'orihttp.{new_func}(w)\n\t\t{dedented}'

            new_content = re.sub(pattern, make_replacement_no_msg, content, flags=re.MULTILINE | re.DOTALL)
            if new_content != content:
                count += len(re.findall(pattern, content, flags=re.MULTILINE | re.DOTALL))
                content = new_content

            # Also handle clean pattern (no embedded comments)
            pattern_clean = (
                r'if (?:respErr|err) := orihttp\.' + old_func + r'\(w\); (?:respErr|err) != nil \{\s*'
                r'logger\.Error\("Failed to write response", logger\.Fields\{"error": (?:respErr|err)\}\)\s*'
                r'\}'
            )
            replacement_clean = f'orihttp.{new_func}(w)'
            new_content, n = re.subn(pattern_clean, replacement_clean, content, flags=re.MULTILINE | re.DOTALL)
            if n > 0:
                count += n
                content = new_content
        else:
            # With message parameter - malformed pattern
            pattern = (
                r'if (?:respErr|err) := orihttp\.' + old_func + r'\(w, ([^)]+)\); (?:respErr|err) != nil \{\s*'
                r'logger\.\s*'
                r'((?:\s*//[^\n]*\n)+)\s*'  # Capture comments
                r'Error\("Failed to write response", logger\.Fields\{"error": (?:respErr|err)\}\)\s*'
                r'\}'
            )
            def make_replacement(match, new_func=new_func):
                msg = match.group(1)
                comments = match.group(2).strip()
                # Dedent comments
                lines = comments.split('\n')
                dedented = '\n'.join(line.strip() for line in lines if line.strip())
                return f'orihttp.{new_func}(w, {msg})\n\t\t{dedented}'

            new_content = re.sub(pattern, make_replacement, content, flags=re.MULTILINE | re.DOTALL)
            if new_content != content:
                count += len(re.findall(pattern, content, flags=re.MULTILINE | re.DOTALL))
                content = new_content

            # Also handle clean pattern (no embedded comments)
            pattern_clean = (
                r'if (?:respErr|err) := orihttp\.' + old_func + r'\(w, ([^)]+)\); (?:respErr|err) != nil \{\s*'
                r'logger\.Error\("Failed to write response", logger\.Fields\{"error": (?:respErr|err)\}\)\s*'
                r'\}'
            )
            replacement_clean = rf'orihttp.{new_func}(w, \1)'
            new_content, n = re.subn(pattern_clean, replacement_clean, content, flags=re.MULTILINE | re.DOTALL)
            if n > 0:
                count += n
                content = new_content

    # Handle RespondValidationError (3 parameters) - malformed
    pattern = (
        r'if (?:respErr|err) := orihttp\.RespondValidationError\(w, ([^,]+), ([^)]+)\); (?:respErr|err) != nil \{\s*'
        r'logger\.\s*'
        r'((?:\s*//[^\n]*\n)+)\s*'
        r'Error\("Failed to write response", logger\.Fields\{"error": (?:respErr|err)\}\)\s*'
        r'\}'
    )
    def make_validation_replacement(match):
        field = match.group(1)
        msg = match.group(2)
        comments = match.group(3).strip()
        lines = comments.split('\n')
        dedented = '\n'.join(line.strip() for line in lines if line.strip())
        return f'orihttp.ValidationError(w, {field}, {msg})\n\t\t{dedented}'

    new_content = re.sub(pattern, make_validation_replacement, content, flags=re.MULTILINE | re.DOTALL)
    if new_content != content:
        count += len(re.findall(pattern, content, flags=re.MULTILINE | re.DOTALL))
        content = new_content

    # Clean pattern
    pattern_clean = (
        r'if (?:respErr|err) := orihttp\.RespondValidationError\(w, ([^,]+), ([^)]+)\); (?:respErr|err) != nil \{\s*'
        r'logger\.Error\("Failed to write response", logger\.Fields\{"error": (?:respErr|err)\}\)\s*'
        r'\}'
    )
    new_content, n = re.subn(pattern_clean, r'orihttp.ValidationError(w, \1, \2)', content, flags=re.MULTILINE | re.DOTALL)
    if n > 0:
        count += n
        content = new_content

    if content != original:
        filepath.write_text(content)
        return count, True

    return 0, False


def main():
    # Find all Go files in internal/ directory
    internal_path = Path(__file__).parent.parent / 'internal'

    if not internal_path.exists():
        print(f"Error: {internal_path} not found")
        sys.exit(1)

    total_changes = 0
    modified_files = []

    for go_file in internal_path.rglob('*.go'):
        if '_test.go' in go_file.name:
            continue

        count, modified = fix_file(go_file)
        if modified:
            total_changes += count
            modified_files.append((go_file, count))
            print(f"  {go_file.relative_to(internal_path.parent)}: {count} changes")

    print(f"\nTotal: {total_changes} changes across {len(modified_files)} files")

    if modified_files:
        print("\nModified files:")
        for f, c in sorted(modified_files, key=lambda x: -x[1]):
            print(f"  {f.relative_to(internal_path.parent)}: {c}")


if __name__ == '__main__':
    main()
