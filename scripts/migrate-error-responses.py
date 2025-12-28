#!/usr/bin/env python3
"""
Migrate verbose error response patterns to fire-and-forget versions.

Before:
    if respErr := orihttp.RespondBadRequest(w, "message"); respErr != nil {
        logger.Error("Failed to write response", logger.Fields{"error": respErr})
    }

After:
    orihttp.BadRequest(w, "message")

Also handles:
    if err := orihttp.RespondBadRequest(w, "message"); err != nil {
        logger.Error("Failed to write response", logger.Fields{"error": err})
    }
"""

import re
import sys
from pathlib import Path


def migrate_file(filepath: Path) -> tuple[int, bool]:
    """Migrate a single file. Returns (count of changes, whether file was modified)."""
    content = filepath.read_text()
    original = content

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

    count = 0

    for old_func, new_func in migrations.items():
        # Pattern for functions with message parameter
        # Matches both respErr and err variable names
        # Allow for comments and blank lines between the if and logger.Error
        if old_func == 'RespondMethodNotAllowed':
            # No message parameter
            pattern = (
                r'if (?:respErr|err) := orihttp\.' + old_func + r'\(w\); (?:respErr|err) != nil \{\s*'
                r'(?://[^\n]*\n\s*)*'  # Allow optional comments
                r'logger\.Error\("Failed to write response", logger\.Fields\{"error": (?:respErr|err)\}\)\s*'
                r'\}'
            )
            replacement = f'orihttp.{new_func}(w)'
        else:
            # With message parameter
            pattern = (
                r'if (?:respErr|err) := orihttp\.' + old_func + r'\(w, ([^)]+)\); (?:respErr|err) != nil \{\s*'
                r'(?://[^\n]*\n\s*)*'  # Allow optional comments
                r'logger\.Error\("Failed to write response", logger\.Fields\{"error": (?:respErr|err)\}\)\s*'
                r'\}'
            )
            replacement = rf'orihttp.{new_func}(w, \1)'

        new_content, n = re.subn(pattern, replacement, content, flags=re.MULTILINE | re.DOTALL)
        if n > 0:
            count += n
            content = new_content

    # Also handle RespondValidationError which has 3 parameters
    pattern = (
        r'if (?:respErr|err) := orihttp\.RespondValidationError\(w, ([^,]+), ([^)]+)\); (?:respErr|err) != nil \{\s*'
        r'(?://[^\n]*\n\s*)*'
        r'logger\.Error\("Failed to write response", logger\.Fields\{"error": (?:respErr|err)\}\)\s*'
        r'\}'
    )
    new_content, n = re.subn(pattern, r'orihttp.ValidationError(w, \1, \2)', content, flags=re.MULTILINE | re.DOTALL)
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

        count, modified = migrate_file(go_file)
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
