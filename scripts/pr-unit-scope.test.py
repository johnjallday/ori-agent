import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('scope', Path(__file__).with_name('pr-unit-scope.py'))
scope = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scope)
ROOT = Path('/repo')


def pkg(name, **fields):
    return dict(Dir=str(ROOT / 'internal' / name), ImportPath=name, GoFiles=['prod.go'],
                TestGoFiles=['unit_test.go'], **fields)


class PRScope(unittest.TestCase):
    def test_path_policy(self):
        for path in ['go.sum', 'Makefile', 'internal/web/assets.go.bak', 'scripts/run.sh',
                     '.github/workflows/ci.yml', 'internal/skills/SKILL.md', 'docs/config.json',
                     'docs/architecture/contract.md']:
            self.assertEqual(scope.path_mode(['README.md', path]), 'full', path)
        self.assertEqual(scope.path_mode([]), 'full')
        self.assertEqual(scope.path_mode(['README.md', 'docs/runbook.md']), 'docs')
        self.assertEqual(scope.path_mode(['README.md', 'internal/a/prod.go']), 'candidate')
        self.assertEqual(scope.path_mode(['internal/web/static/site.css']), 'candidate')

    def test_reverse_imports(self):
        packages = [pkg('c', XTestImports=['b']), pkg('b', Imports=['a']), pkg('a'), pkg('other')]
        self.assertEqual(scope.affected(['internal/a/prod.go'], packages, ROOT), ['a', 'b', 'c'])
        self.assertEqual(scope.affected(['internal/a/unit_test.go'], packages, ROOT), ['a'])

    def test_uncertain_or_wide_falls_back(self):
        for packages in [[], [pkg('a', IgnoredGoFiles=['a_darwin.go'])], [pkg('a', CgoFiles=['c.go'])],
                         [pkg('a')] + [pkg(str(i), Imports=['a']) for i in range(scope.MAX_PACKAGES)]]:
            self.assertIsNone(scope.affected(['internal/a/prod.go'], packages, ROOT))
        self.assertIsNone(scope.affected(['internal/a/deleted.go'], [pkg('a')], ROOT))

    def test_real_diff_and_scoped_execution(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            script = Path(__file__).resolve().with_name('pr-unit-scope.py')
            (root / 'scripts').mkdir()
            shutil.copy2(script.with_name('list-unit-packages.sh'), root / 'scripts')
            package = root / 'internal' / 'example'
            package.mkdir(parents=True)
            (package / 'example.go').write_text('package example\n')
            (root / 'go.mod').write_text('module example.org/prscope\n\ngo 1.22\n')
            output = root / '.git' / 'output'
            env = dict(os.environ, GITHUB_OUTPUT=str(output), GOWORK='off', GOPROXY='off')

            def run(*argv, check=True):
                return subprocess.run(argv, cwd=root, env=env, check=check, text=True,
                                      stdout=subprocess.PIPE, stderr=subprocess.STDOUT)

            run('git', 'init', '-q')

            def commit():
                run('git', 'add', '.')
                run('git', '-c', 'user.name=Test', '-c', 'user.email=test@example.org',
                    '-c', 'commit.gpgsign=false', 'commit', '-qm', 'fixture')

            commit()
            base = run('git', 'rev-parse', 'HEAD').stdout.strip()
            test = package / 'example_test.go'
            test.write_text('package example\nimport "testing"\nfunc TestGate(t *testing.T) {}\n')
            commit()
            for stage, mode in [('paths', 'candidate'), ('test', 'affected')]:
                result = run(sys.executable, script, stage, base, check=False)
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertEqual(output.read_text().splitlines()[-1], 'mode=' + mode)
            test.write_text(test.read_text().replace('{}', '{ t.Fatal("must fail") }'))
            commit()
            output.unlink()
            self.assertNotEqual(run(sys.executable, script, 'test', base, check=False).returncode, 0)
            self.assertFalse(output.exists(), 'failed unit tests cannot publish a successful mode')

    def test_embedded_asset(self):
        packages = [pkg('web'), pkg('app', Imports=['web'])]
        self.assertEqual(scope.affected(['internal/web/templates/page.html'], packages, ROOT), ['app', 'web'])


if __name__ == '__main__':
    unittest.main()
