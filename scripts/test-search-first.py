#!/usr/bin/env python3
"""Credential-free search-first acceptance. No opens, GUI, or existing profiles."""
import argparse
import contextlib
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

REPO = Path(__file__).resolve().parents[1]
FIXTURE = REPO / 'scripts/search-first-fixture.py'
EVIDENCE = REPO / 'tasks/evidence-search-first-onboarding'
# Repository source audits skip testdata; fixtures may intentionally contain unreadable directories.
SCRATCH = REPO / 'bin/testdata/search-first'


def fixture(large=False):
    return Path(subprocess.check_output([sys.executable, str(FIXTURE), str(SCRATCH)] + (['--large'] if large else []), text=True).strip())


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def api(url, path, body=None):
    req = urllib.request.Request(url + path, data=None if body is None else json.dumps(body).encode(), headers={'Origin': url, 'Content-Type': 'application/json', 'X-Ori-Search': '1'})
    with urllib.request.urlopen(req, timeout=10) as r:
        return r.read()


@contextlib.contextmanager
def serve(root, rev=None, build_tmp=None):
    assert (root/'.search-first-fixture').read_text() == 'synthetic-v1\n'
    number = port()
    url = f'http://localhost:{number}'
    (root/'.search-first-test-url').write_text(url+'\n')
    env = {k: os.environ[k] for k in ('PATH','HOME','TMPDIR','GOCACHE','GOMODCACHE','GOPATH') if k in os.environ}
    env.update(GOWORK='off', ORI_DEMO_SPLIT_PROFILE='1', HTTP_PROXY='http://127.0.0.1:1', HTTPS_PROXY='http://127.0.0.1:1', NO_PROXY='localhost,127.0.0.1')
    if rev:
        # git archive and Go scratch never become dependencies on another checkout.
        env['TMPDIR'] = str(build_tmp)
    with (root/'server.log').open('ab') as log:
        p = subprocess.Popen([str(REPO/'scripts/demo-server.sh')] + (['--rev',rev] if rev else []) + [str(number),str(root)], cwd=REPO, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log)
        try:
            for _ in range(450):
                if p.poll() is not None:
                    raise RuntimeError('isolated launch failed; see the fixture server.log')
                try:
                    if json.loads(api(url,'/health'))['status']=='ok':break
                except OSError: pass
                time.sleep(.2)
            else:raise RuntimeError('isolated startup timed out')
            yield url
        finally:
            p.terminate(); p.wait(timeout=30)


def spec(root,url,scenario,name,browser='chromium'):
    env=dict(os.environ,PLAYWRIGHT_BASE_URL=url,ORI_SEARCH_FIRST_FIXTURE=str(root),ORI_SEARCH_FIRST_SCENARIO=scenario,ORI_SEARCH_EVIDENCE=str(EVIDENCE))
    config=['--config','playwright.files-map.config.ts'] if scenario=='navigation' else []
    subprocess.run([str(REPO/'node_modules/.bin/playwright'),'test',*config,'--project',browser,'--reporter=list','tests/'+name+'.spec.ts'],cwd=REPO,env=env,check=True)


def upgrades():
    build_tmp = Path(tempfile.mkdtemp(prefix='search-prior-', dir=SCRATCH))
    for established in (False,True):
        root=fixture()
        with serve(root,'906007fe2',build_tmp) as url:
            api(url,'/api/onboarding/step',{'step_name':'names'})
            if established:
                api(url,'/api/onboarding/complete',{})
                result=json.loads(api(url,'/api/personal-hq/setup',{'name':'Synthetic Harbor HQ','timezone':'UTC','schedule_days':['mon'],'schedule_time':'09:00','scope':'selected','selected_workspace_ids':[],'notify_on_ready':False}))
                hq=result['status']['workspace_id'];assert hq
        with serve(root) as url:
            assert b'home-command-bridge' in api(url,'/')
            assert json.loads(api(url,'/api/onboarding/status'))['current_step']==1
            assert json.loads(api(url,'/api/file-search/sources'))==[]
            if established:
                assert json.loads(api(url,'/api/personal-hq/status'))['status']['workspace_id']==hq
                spec(root,url,'established','search-first-palette')
            print('Preserved prior-version '+('HQ' if established else 'partial setup'),flush=True)


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--upgrades',action='store_true',help='also build the prior-version fixture')
    args=parser.parse_args()
    if sys.platform!='darwin':sys.exit('macOS only')
    SCRATCH.mkdir(parents=True,exist_ok=True);EVIDENCE.mkdir(parents=True,exist_ok=True)
    for scenario in ('fresh','skip'):
        root=fixture(large=scenario=='fresh')
        with serve(root) as url:spec(root,url,scenario,'search-first-onboarding')
        if scenario=='fresh':
            with serve(root) as url:spec(root,url,'return','search-first-return')
    for browser in ('chromium','webkit'):
        root=fixture()
        with serve(root) as url:spec(root,url,'navigation','files-map-navigation',browser)
    if args.upgrades:upgrades()
