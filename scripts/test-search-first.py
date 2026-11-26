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
# The build before folders were managed in Files (merge-tree-into-files PR 1).
PRIOR_FILES = '02f3c35e5'


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
def serve(root, rev=None, build_tmp=None, fixture_binary=None):
    assert (root/'.search-first-fixture').read_text() == 'synthetic-v1\n'
    number = port()
    url = f'http://localhost:{number}'
    (root/'.search-first-test-url').write_text(url+'\n')
    env = {k: os.environ[k] for k in ('PATH','HOME','TMPDIR','GOCACHE','GOMODCACHE','GOPATH') if k in os.environ}
    env.update(GOWORK='off', ORI_DEMO_REQUIRE_FREE_PORT='1', ORI_DEMO_SPLIT_PROFILE='1', HTTP_PROXY='http://127.0.0.1:1', HTTPS_PROXY='http://127.0.0.1:1', NO_PROXY='localhost,127.0.0.1')
    if rev:
        # git archive and Go scratch never become dependencies on another checkout.
        env['TMPDIR'] = str(build_tmp)
    with (root/'server.log').open('ab') as log:
        command = [str(REPO/'scripts/demo-server.sh')] + (['--rev',rev] if rev else []) + [str(number),str(root)]
        cwd = REPO
        if fixture_binary:
            assert not rev and fixture_binary.is_relative_to(SCRATCH)
            cwd = root
            command = [str(fixture_binary)]
            env.update(HOME=str(root/'home'), XDG_CONFIG_HOME=str(root/'home/.config'), ORI_DATA_DIR=str(root/'data'), PORT=str(number), NO_BROWSER='1', ORI_NO_DESKTOP_OPEN='1')
        p = subprocess.Popen(command, cwd=cwd, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log)
        try:
            for _ in range(450):
                if p.poll() is not None:
                    raise RuntimeError('isolated launch failed; see the fixture server.log')
                try:
                    # demo-server execs the binary in place. Never seed a
                    # profile unless this exact process owns the listener.
                    owners = subprocess.run(['lsof','-nP','-t',f'-iTCP:{number}','-sTCP:LISTEN'], capture_output=True,text=True).stdout.split()
                    if owners and owners != [str(p.pid)]:
                        raise RuntimeError('fixture port belongs to another process')
                    if owners and json.loads(api(url,'/health'))['status']=='ok':break
                except OSError: pass
                time.sleep(.2)
            else:raise RuntimeError('isolated startup timed out')
            yield url
        finally:
            p.terminate(); p.wait(timeout=30)


def spec(root,url,scenario,name,browser='chromium'):
    env=dict(os.environ,PLAYWRIGHT_BASE_URL=url,ORI_SEARCH_FIRST_FIXTURE=str(root),ORI_SEARCH_FIRST_SCENARIO=scenario,ORI_SEARCH_EVIDENCE=str(EVIDENCE))
    config=['--config','playwright.files-map.config.ts'] if scenario in ('navigation', 'files-search', 'folder-backed','folder-return','folder-assistant') or scenario.startswith('parity-') else []
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
    # From the build before folders were managed in Files (merge-tree-into-files
    # PR 1) to this one: a search-only profile and a workspace-only profile load
    # with their folders, indexes and links as they were, and nothing is converted.
    def snapshot(url):
        sources=json.loads(api(url,'/api/file-search/sources'))
        links={}
        for w in json.loads(api(url,'/api/workspaces'))['folders']:
            dirs=json.loads(api(url,'/api/workspaces/'+w['id']+'/directories')).get('directories') or []
            links[w['id']]=sorted((d['id'],d['path']) for d in dirs)
        return sources,links
    def settled(url):
        for _ in range(150):
            if all(s['state'] not in ('queued','mapping') for s in json.loads(api(url,'/api/file-search/sources'))):return
            time.sleep(.2)
        raise RuntimeError('indexing did not settle')
    for shape in ('search-only','workspace-only'):
        root=fixture()
        with serve(root,PRIOR_FILES,build_tmp) as url:
            if shape=='search-only':
                intent=json.loads(api(url,'/api/file-search/begin',{'kind':'documents'}))
                api(url,'/api/file-search/select',intent)
            else:
                api(url,'/api/onboarding/skip',{})
                ws=json.loads(api(url,'/api/workspaces',{'name':'Upgrade sketches'}))['folder']['id']
                api(url,'/api/workspaces/'+ws+'/directories',{'name':'Downloads','path':str(root/'home/Downloads')})
            settled(url)
            before=snapshot(url)
        with serve(root) as url:
            after=snapshot(url)
            assert after==before,(shape,before,after)
            if shape=='search-only':
                assert len(after[0])==1 and 'workspace' not in after[0][0],after
            else:
                assert after[0]==[] and any(after[1].values()),after
        print('Preserved PR 1 '+shape+' profile',flush=True)


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--upgrades',action='store_true',help='also build the prior-version fixture')
    parser.add_argument('--folder-backed',action='store_true',help='run only reviewed folder setup on established split profiles')
    parser.add_argument('--browser',choices=('chromium','webkit'),action='append',help='limit the browser journeys to this browser (repeatable; default both)')
    parser.add_argument('--scenario',choices=('fresh','skip','navigation','files-search','folder-assistant'),action='append',help='run only these default scenarios while iterating (repeatable; default all; not with --folder-backed)')
    args=parser.parse_args()
    BROWSERS=tuple(args.browser or ('chromium','webkit'))
    SCENARIOS=set(args.scenario or ('fresh','skip','navigation','files-search'))
    if sys.platform!='darwin':sys.exit('macOS only')
    SCRATCH.mkdir(parents=True,exist_ok=True);EVIDENCE.mkdir(parents=True,exist_ok=True)
    if args.folder_backed or SCENARIOS == {'folder-assistant'}:
        def project_fixture():
            root=fixture()
            project=root/'home/Synthetic Project'
            project.mkdir()
            (project/'Harbor.txt').write_text('Synthetic project fixture.\n')
            (root/'home/Downloads/Ship.txt').write_text('Synthetic download.\n')
            (root/'data').mkdir(mode=0o700)
            return root
        build=Path(tempfile.mkdtemp(prefix='picker-build-',dir=SCRATCH))
        overlay=build/'overlay.json'
        server = REPO/'internal/server'
        overlay.write_text(json.dumps({'Replace': {
            str(server/'search_folder_picker.go'): str(server/'testdata/search_folder_picker.go'),
            str(server/'folder_agent_model.go'): str(server/'testdata/folder_agent_model.go.fixture'),
        }}))
        binary=build/'fixture-server'
        subprocess.run(['go','build','-overlay',str(overlay),'-o',str(binary),'./cmd/server'],cwd=REPO,env={**os.environ,'GOWORK':'off'},check=True)
        def assistant_journey(browser):
            root=project_fixture()
            albums=root/'home/Albums'
            albums.mkdir()
            (albums/'Daybreak.wav').write_text('private-content-sentinel\n')
            with serve(root,fixture_binary=binary) as url:
                spec(root,url,'folder-assistant','folder-backed-search-locations',browser)
        for browser in BROWSERS:
            if not args.folder_backed:
                assistant_journey(browser)
                continue
            root=project_fixture()
            with serve(root, fixture_binary=binary) as url:
                # First-run search consent predates established workspace setup.
                intent=json.loads(api(url,'/api/file-search/begin',{'kind':'downloads'}))
                api(url,'/api/file-search/select',intent)
                spec(root,url,'folder-backed','folder-backed-search-locations',browser)
            with serve(root, fixture_binary=binary) as url:
                spec(root,url,'folder-return','folder-backed-search-locations',browser)
            with serve(root, fixture_binary=binary) as url:
                locations=json.loads(api(url,'/api/file-search/sources'))
                assert sorted(s['label'] for s in locations)==['Documents','Downloads']
                assert len([w for w in json.loads(api(url,'/api/workspaces'))['folders'] if w['name']=='Research archive'])==1
            assistant_journey(browser)
            for entry in ('files','workspace'):
                for kind in ('downloads','documents','custom'):
                    root=project_fixture()
                    with serve(root,fixture_binary=binary) as url:
                        spec(root,url,'parity-'+entry+'-'+kind,'folder-setup-parity',browser)
        binary.unlink()  # evidence is the fixture, not its disposable executable
        sys.exit(0)
    for scenario in ('fresh','skip'):
        if scenario not in SCENARIOS:continue
        root=fixture(large=scenario=='fresh')
        with serve(root) as url:spec(root,url,scenario,'search-first-onboarding')
        if scenario=='fresh':
            with serve(root) as url:spec(root,url,'return','search-first-return')
    for browser in BROWSERS:
        if 'navigation' not in SCENARIOS:break
        root=fixture()
        with serve(root) as url:spec(root,url,'navigation','files-map-navigation',browser)
    # Files in searchable folders, found from Files' own search box.
    for browser in BROWSERS:
        if 'files-search' not in SCENARIOS:break
        root=fixture()
        with serve(root) as url:spec(root,url,'files-search','files-search-folders',browser)
    if args.upgrades:upgrades()
