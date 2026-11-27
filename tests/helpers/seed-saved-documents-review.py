"""Seed an old, already-kept Documents view in a disposable fresh-server profile.

Only the historical sidecar is a fixture. Filing access, scan matching, File,
journal and Undo in the browser journey are the production paths. Use Python
integers to retain the original nanosecond timestamps in the JSON record.
"""
import datetime
import hashlib
import json
import pathlib
import sys
import uuid

sandbox = pathlib.Path(sys.argv[1]).resolve(strict=True)
wid = str(uuid.UUID(sys.argv[2]))
assert sandbox.name.startswith(('ori-e2e.', 'ori-demo.'))
assert (sandbox / '.ori-demo-owner').read_text().strip() == f'worktree={pathlib.Path.cwd().resolve()}'
paths = [p for p in sandbox.rglob('folder-agent-v1.json')
         if p.resolve(strict=True) == p and json.loads(p.read_text()).get('workspace_id') == wid]
assert len(paths) == 1
sidecar, = paths
record = json.loads(sidecar.read_text())
assert not record.get('organization_runs') and record['state'] == 'reported'
if sys.argv[3:] == ['pre-station-hire']:
    # Ordinary historical hires did not retain the station's count/binding.
    # Keep the original report, grant, lead, root witness and folder intact.
    assert not record.get('assigned_link') and record['loose_documents']['station_binding']
    del record['loose_documents']
    sidecar.write_text(json.dumps(record))
    sys.exit(0)
assert not sys.argv[3:]
root = sandbox / 'home' / 'Documents'
assert root.resolve(strict=True) == root
creation = json.load(sys.stdin)
lead, = [a for a in creation['agent_instances'] if a.get('entry_point')]
ref, = creation['directory_references']
grant, = creation['mcp_bindings']
assert creation['id'] == wid and ref['path'] == str(root) and lead['name'] == record['agent_name']
branch, = [f for f in record['map']['folders'] if not f['path']]
parts = [record['owner_user_id'], wid, str(root), branch['branch_witness'], lead['name'], lead['id'], ref['id'], grant['id']]
authority = hashlib.sha256('\0'.join(parts).encode()).hexdigest()
name = 'note-4.txt'
st = (root / name).stat()
source = dict(ref='old-note', path=name, version=dict(Identity=f'{st.st_dev}:{st.st_ino}', Bytes=st.st_size,
              Modified=st.st_mtime_ns, Changed=st.st_ctime_ns, Links=st.st_nlink, Placeholder=False))
record['organization_runs'] = [dict(authority=authority, selection='original', result=dict(
    id='a' * 64, revision=2, status='reviewed', processor=dict(name='Earlier processor', model='Earlier model'),
    created=datetime.datetime.now(datetime.timezone.utc).isoformat(), read=1, skipped=0, remaining=0,
    files=[dict(source=source, status='read')], groups=[dict(id='saved', title='Earlier trip notes', kept=True,
    members=['old-note'], reasons={'old-note': 'Confirmed on the earlier review.'})]))]
sidecar.write_text(json.dumps(record))
