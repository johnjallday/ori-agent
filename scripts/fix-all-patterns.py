#!/usr/bin/env python3
"""
Comprehensive fix for all verbose error patterns and malformed logger calls.
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

    # Pattern 1: Malformed logger with embedded comments (split across lines)
    # Use a more flexible pattern that handles nested parentheses
    for old_func, new_func in migrations.items():
        if old_func == 'RespondMethodNotAllowed':
            # No message parameter - malformed
            pattern = (
                r'if (?:respErr|err|encodeErr) := orihttp\.' + old_func + r'\(w\); (?:respErr|err|encodeErr) != nil \{\s*'
                r'logger\.\s*'
                r'((?:\s*//[^\n]*\n|\s*\n)+)\s*'
                r'Error\([^)]+\)\s*'
                r'\}'
            )
            def make_replacement_no_msg(match, new_func=new_func):
                comments = match.group(1).strip()
                lines = [l.strip() for l in comments.split('\n') if l.strip() and l.strip().startswith('//')]
                if lines:
                    return f'orihttp.{new_func}(w)\n\t\t' + '\n\t\t'.join(lines)
                return f'orihttp.{new_func}(w)'

            new_content = re.sub(pattern, make_replacement_no_msg, content, flags=re.MULTILINE | re.DOTALL)
            if new_content != content:
                count += 1
                content = new_content
        else:
            # With message parameter - malformed (handle nested parens with .*?)
            pattern = (
                r'if (?:respErr|err|encodeErr) := orihttp\.' + old_func + r'\(w, (.*?)\); (?:respErr|err|encodeErr) != nil \{\s*'
                r'logger\.\s*'
                r'((?:\s*//[^\n]*\n|\s*\n)+)\s*'
                r'Error\([^}]+\}\)\s*'
                r'\}'
            )
            def make_replacement(match, new_func=new_func):
                msg = match.group(1)
                comments = match.group(2).strip()
                lines = [l.strip() for l in comments.split('\n') if l.strip() and l.strip().startswith('//')]
                if lines:
                    return f'orihttp.{new_func}(w, {msg})\n\t\t' + '\n\t\t'.join(lines)
                return f'orihttp.{new_func}(w, {msg})'

            new_content = re.sub(pattern, make_replacement, content, flags=re.MULTILINE | re.DOTALL)
            if new_content != content:
                count += 1
                content = new_content

    # Pattern 2: Clean verbose patterns (no embedded comments)
    for old_func, new_func in migrations.items():
        if old_func == 'RespondMethodNotAllowed':
            pattern = (
                r'if (?:respErr|err|encodeErr) := orihttp\.' + old_func + r'\(w\); (?:respErr|err|encodeErr) != nil \{\s*'
                r'logger\.Error\("[^"]*"[^}]+\}\)\s*'
                r'\}'
            )
            replacement = f'orihttp.{new_func}(w)'
        else:
            # Handle nested parentheses with .*? and match until closing brace of logger.Fields
            pattern = (
                r'if (?:respErr|err|encodeErr) := orihttp\.' + old_func + r'\(w, (.*?)\); (?:respErr|err|encodeErr) != nil \{\s*'
                r'logger\.Error\("[^"]*"[^}]+\}\)\s*'
                r'\}'
            )
            replacement = rf'orihttp.{new_func}(w, \1)'

        new_content, n = re.subn(pattern, replacement, content, flags=re.MULTILINE | re.DOTALL)
        if n > 0:
            count += n
            content = new_content

    # Pattern 3: RespondValidationError (3 parameters)
    pattern = (
        r'if (?:respErr|err) := orihttp\.RespondValidationError\(w, ([^,]+), ([^)]+)\); (?:respErr|err) != nil \{\s*'
        r'(?:logger\.\s*(?:\s*//[^\n]*\n|\s*\n)*\s*Error\([^)]+\)|logger\.Error\([^)]+\))\s*'
        r'\}'
    )
    new_content, n = re.subn(pattern, r'orihttp.ValidationError(w, \1, \2)', content, flags=re.MULTILINE | re.DOTALL)
    if n > 0:
        count += n
        content = new_content

    # Pattern 4: Misplaced comments after fire-and-forget calls
    # Fix: orihttp.BadRequest(w, "msg")\n// comment\nreturn -> orihttp.BadRequest(w, "msg")\nreturn\n\n// comment
    # Actually, keep comments where they are but ensure proper structure

    if content != original:
        filepath.write_text(content)
        return count, True

    return 0, False


def main():
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


if __name__ == '__main__':
    main()
