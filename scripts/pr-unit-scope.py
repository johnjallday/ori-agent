"""Select a conservative fast unit gate for ordinary PRs into dev only."""
import json
import os
from pathlib import Path
import subprocess
import sys

# At this point prefer the parallel full suite to one overloaded runner.
MAX_PACKAGES = 12


def path_mode(paths):
    candidate = False
    for path in paths:
        # Architecture contracts can be inputs to Go tests that inspect prose
        # directly, e.g. TestNoReaperDomainLanguageOutsidePlugin.
        if path.startswith('docs/architecture/'):
            return 'full'
        if path == 'README.md' or (path.startswith('docs/') and path.endswith('.md')):
            continue
        if path.startswith('internal/') and path.endswith('.go'):
            candidate = True
        elif path.startswith(('internal/web/templates/', 'internal/web/static/')):
            candidate = True  # Embedded by internal/web.
        else:
            return 'full'
    return ('candidate' if candidate else 'docs') if paths else 'full'


def affected(paths, packages, root):
    by_dir = {Path(p['Dir']).relative_to(root).as_posix(): p for p in packages}
    selected, production = set(), set()
    for path in paths:
        if path.startswith('docs/') or path == 'README.md':
            continue
        directory, name = path.rsplit('/', 1)
        embedded = path.startswith(('internal/web/static/', 'internal/web/templates/'))
        p = by_dir.get('internal/web' if embedded else directory)
        if not p:
            return None
        selected.add(p['ImportPath'])
        if not embedded:
            # Unknown (including removed, tagged-out, or native) sources and
            # packages with platform variants are not a safe Linux-only change.
            if p.get('IgnoredGoFiles') or p.get('CgoFiles'):
                return None
            if name in p.get('TestGoFiles', []) + p.get('XTestGoFiles', []):
                continue  # Changes to tests do not propagate to importers.
            if name not in p.get('GoFiles', []):
                return None
        production.add(p['ImportPath'])

    # Transitive reverse imports, including imports only used by tests. Do
    # not propagate test-only edges: their callers' binaries are unchanged.
    while True:
        before = len(production)
        for p in packages:
            if production.intersection(p.get('Imports', [])):
                production.add(p['ImportPath'])
            if production.intersection(p.get('TestImports', []) + p.get('XTestImports', [])):
                selected.add(p['ImportPath'])
        if before == len(production):
            break
    names = selected | production
    return sorted(names) if 0 < len(names) <= MAX_PACKAGES else None


def main():
    stage, base = sys.argv[1:]
    if stage not in ('paths', 'test'):
        sys.exit('expected paths or test')
    # CI checks out the synthetic merge, not the PR tip. Compare both ends of
    # renames against the exact base used to construct that tested tree.
    raw = subprocess.check_output(['git', 'diff', '--no-renames', '--name-only', '-z', base, 'HEAD', '--'])
    paths = list(map(os.fsdecode, raw.rstrip(b'\0').split(b'\0'))) if raw else []
    mode = path_mode(paths)
    if stage == 'test':
        if mode != 'candidate':
            sys.exit('diff no longer matches candidate scope')
        fields = 'Dir,ImportPath,Imports,TestImports,XTestImports,GoFiles,TestGoFiles,XTestGoFiles,IgnoredGoFiles,CgoFiles'
        units = subprocess.check_output(['./scripts/list-unit-packages.sh'], text=True).splitlines()
        if not units:
            sys.exit('empty canonical unit package list')
        data = subprocess.check_output(['go', 'list', '-race', '-json=' + fields, *units], text=True)
        decoder, packages = json.JSONDecoder(), []
        root = Path.cwd()
        pos = 0
        while pos < len(data):
            if data[pos].isspace():
                pos += 1
                continue
            p, pos = decoder.raw_decode(data, pos)
            packages.append(p)
        names = affected(paths, packages, root)
        if names:
            print('Affected unit packages:', *names, sep='\n', flush=True)
            subprocess.run(['go', 'test', '-short', '-race', '-count=1', '-timeout', '30m', *names], check=True)
            mode = 'affected'
        else:
            mode = 'full'
    print('Unit scope:', mode)
    with open(os.environ['GITHUB_OUTPUT'], 'a') as out:
        print('mode=' + mode, file=out)


if __name__ == '__main__':
    main()
