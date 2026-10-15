#!/usr/bin/env python3
"""Create only the fresh database's maintenance-role env file on the host.

Run as root ONLY after the reviewed host operation is authorized. Never prints
credentials and refuses an existing target; it neither rotates nor edits roles.
"""

import os
from pathlib import Path
import re
import secrets
import stat
import sys

TARGET = Path('/etc/mycfc/database-maintenance.env')
MEDIA_ROLE = 'mycfc_media_cleanup_login'
RETENTION_ROLE = 'mycfc_data_retention_login'
IDENTIFIER = re.compile(r'[a-z][a-z0-9_]{0,62}\Z')


def content(media_role: str = MEDIA_ROLE, retention_role: str = RETENTION_ROLE) -> str:
    if (not IDENTIFIER.fullmatch(media_role) or
            not IDENTIFIER.fullmatch(retention_role) or
            media_role == retention_role):
        raise ValueError('maintenance login role identifiers rejected')
    return (f'MEDIA_CLEANUP_DB_USER={media_role}\n'
            f'MEDIA_CLEANUP_DB_PASSWORD={secrets.token_urlsafe(48)}\n'
            f'DATA_RETENTION_DB_USER={retention_role}\n'
            f'DATA_RETENTION_DB_PASSWORD={secrets.token_urlsafe(48)}\n')


def create(path: Path, data: str) -> None:
    # Exclusive and no-follow: never overwrite an existing credential file.
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, 'w', encoding='ascii') as file:
            file.write(data)
            file.flush()
            os.fsync(file.fileno())
    except BaseException:
        path.unlink(missing_ok=True)
        raise


def main() -> int:
    if sys.argv[1:] != ['--create']:
        print('usage: stage-fresh-db-maintenance-env.py --create', file=sys.stderr)
        return 2
    try:
        parent = TARGET.parent.stat(follow_symlinks=False)
        protected_parent = (stat.S_ISDIR(parent.st_mode) and parent.st_uid == 0 and
                            stat.S_IMODE(parent.st_mode) & 0o022 == 0)
    except OSError:
        protected_parent = False
    if os.geteuid() != 0 or not protected_parent:
        print('refused: root-owned non-writable /etc/mycfc directory required', file=sys.stderr)
        return 1
    try:
        create(TARGET, content())
    except (OSError, ValueError):
        print('refused: maintenance file could not be exclusively staged', file=sys.stderr)
        return 1
    print('maintenance credentials staged in root-only file; values not displayed')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
