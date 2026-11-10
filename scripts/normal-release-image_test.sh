#!/bin/sh
set -eu
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
sh -n "$script_dir/normal-release-image-test.sh"
python3 - "$script_dir" <<'PYTHON'
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

scripts = Path(sys.argv[1])
with tempfile.TemporaryDirectory(prefix='mycfc-normal-image-control-') as folder:
    folder = Path(folder)
    fake = folder / 'bin'
    fake.mkdir()
    (fake / 'git').write_text('''#!/usr/bin/env python3
import sys
if 'rev-parse' in sys.argv:
 print(('b' if sys.argv[-1]=='HEAD^{tree}' else 'a')*40)
''')
    (fake / 'docker').write_text('''#!/usr/bin/env python3
import json,os,pathlib,sys
args=sys.argv[1:];state=pathlib.Path(os.environ['TEST_IMAGE_STATE'])
with open(os.environ['TEST_IMAGE_LOG'],'a') as log:log.write(json.dumps(args)+'\\n')
if args[:2]==['image','inspect']:
 if not state.exists():sys.exit(1)
 print('sha256:'+'d'*64)
elif args[0]=='build':
 assert '--build-arg' in args and 'RELEASE_VERSION=v0.0.0-ci' in args and 'GIT_SHA='+'a'*40 in args
 if os.environ.get('TEST_BUILD_FAILURE')=='1':sys.exit(7)
 state.write_text(args[args.index('--tag')+1])
elif args[:2]==['image','rm']:
 assert state.read_text()==args[-1] and args[-1].startswith('mycfc-normal-release-')
 state.unlink()
elif args[0]=='run':
 assert args[1:4]==['--rm','--network','none'] and args[-2]==state.read_text()
 command=args[-1];digest=os.environ['TEST_SCHEMA_DIGEST']
 if command=='normal-release-contract':
  print(json.dumps({'contract':'mycfc/normal-release/v1','version':'v0.0.0-ci','candidate':'a'*40,'database':'mycfc','parent_digest':'0'*64,'final_digest':digest}))
 elif command=='precision-capabilities':
  print(json.dumps({'protocol':1,'version':'v0.0.0-ci','candidate':('c' if os.environ.get('TEST_BAD_CAP')=='1' else 'a')*40,'reader_generation':5,'writer_generation':2,'snapshot_read':[1,2],'schema_read':['schedule-c1','schedule-p1'],'write':['INTERVAL','START_ONLY','DAY','BAND','snapshot-v1','snapshot-v2']}))
 elif command=='schema-inventory':print(json.dumps(['001_initial','reset-baseline-v1']))
 elif command=='schema-digest':print('f'*64 if os.environ.get('TEST_BAD_DIGEST')=='1' else digest)
 else:sys.exit(8)
else:sys.exit(9)
''')
    for command in fake.iterdir():
        command.chmod(0o755)
    digest = hashlib.sha256(b'001_initial\nreset-baseline-v1').hexdigest()
    for name, setting, success in [('valid', None, True), ('bad-capability-stamp', 'TEST_BAD_CAP', False),
                                   ('bad-inventory-digest', 'TEST_BAD_DIGEST', False),
                                   ('build-failure', 'TEST_BUILD_FAILURE', False)]:
        state, log = folder / (name + '.state'), folder / (name + '.log')
        env = dict(os.environ, PATH=str(fake) + os.pathsep + os.environ['PATH'],
                   TEST_IMAGE_STATE=str(state), TEST_IMAGE_LOG=str(log), TEST_SCHEMA_DIGEST=digest)
        if setting:
            env[setting] = '1'
        result = subprocess.run(['sh', str(scripts / 'normal-release-image-test.sh')],
                                env=env, text=True, capture_output=True)
        assert (result.returncode == 0) == success, (name, result.stdout, result.stderr)
        assert not state.exists(), name
        calls = [json.loads(line) for line in log.read_text().splitlines()]
        assert not any(call[0] in ['network', 'exec', 'login', 'push', 'pull'] for call in calls)
        if success:
            assert 'normal_release_contract=accepted' in result.stdout
            assert 'wrong_stamp=rejected' in result.stdout
        if setting != 'TEST_BUILD_FAILURE':
            assert sum(call[:2] == ['image', 'rm'] for call in calls) == 1
    workflow = (scripts.parent / '.github/workflows/ci.yml').read_text()
    assert '  release-upgrade:\n' in workflow  # Required job identity is stable.
    assert 'run: make test-normal-release-image' in workflow
    assert 'RELEASE_UPGRADE_RESULT: ${{ needs.release-upgrade.result }}' in workflow
    assert 'test "$RELEASE_UPGRADE_RESULT" = success' in workflow
    assert (scripts / 'release-upgrade-test.sh').is_file()  # Historical opt-in kept.
print('normal-release-image control tests passed')
PYTHON
