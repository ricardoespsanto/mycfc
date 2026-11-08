#!/usr/bin/env python3
"""Render a narrow compatibility candidate for the installed MyCFC Compose bundle.

Never edits the input or the production host. The only output is a new local file;
credential values are neither read nor printed. A drifted host bundle is refused.
"""

import argparse
import os
from pathlib import Path
import sys


OLD_BOOTSTRAP = '''  db-bootstrap:
    image: ${MYCFC_IMAGE:?set MYCFC_IMAGE to the immutable ECR image reference in /etc/mycfc/mycfc.env}
    command: ["bootstrap-db"]
    environment:
      <<: *production-config
      PRIVACY_EXECUTOR_DB_USER: ${PRIVACY_EXECUTOR_DB_USER:-}
      PRIVACY_EXECUTOR_DB_PASSWORD: ${PRIVACY_EXECUTOR_DB_PASSWORD:-}
      PRIVACY_ACTIVATION_BROKER_DB_USER: ${PRIVACY_ACTIVATION_BROKER_DB_USER:-}
      PRIVACY_ACTIVATION_BROKER_DB_PASSWORD: ${PRIVACY_ACTIVATION_BROKER_DB_PASSWORD:-}
    depends_on:
      postgres:
        condition: service_healthy
    networks: [mycfc]
    profiles: [release]
'''
OLD_MIGRATE = OLD_BOOTSTRAP.replace('db-bootstrap:', 'migrate:', 1).replace('"bootstrap-db"', '"migrate"', 1)
MAINTENANCE_FILE = '''    env_file:
      - path: ${DATABASE_MAINTENANCE_ENV_FILE:-/etc/mycfc/database-maintenance.env}
        required: true
'''


def render(source: str) -> str:
    if not source.startswith('name: mycfc-production\n'):
        raise ValueError('unexpected Compose project')
    if source.count('      - pgdata:/var/lib/postgresql/data\n') != 1:
        raise ValueError('unexpected PostgreSQL volume mapping')
    if source.count(OLD_BOOTSTRAP) != 1 or source.count(OLD_MIGRATE) != 1:
        raise ValueError('installed bootstrap/migrate stanza drift; no candidate produced')
    # Only bootstrap receives the PostgreSQL superuser password. Migration gets
    # its own credential, never the superuser credential. No web service changes.
    bootstrap = OLD_BOOTSTRAP.replace(
        '    depends_on:\n',
        '      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in /etc/mycfc/mycfc.env}\n'
        '      MIGRATION_DB_PASSWORD: ${MIGRATION_DB_PASSWORD:?set MIGRATION_DB_PASSWORD in /etc/mycfc/mycfc.env}\n'
        + MAINTENANCE_FILE + '    depends_on:\n', 1,
    )
    migrate = OLD_MIGRATE.replace(
        '    depends_on:\n',
        '      MIGRATION_DB_PASSWORD: ${MIGRATION_DB_PASSWORD:?set MIGRATION_DB_PASSWORD in /etc/mycfc/mycfc.env}\n'
        + MAINTENANCE_FILE + '    depends_on:\n', 1,
    )
    return source.replace(OLD_BOOTSTRAP, bootstrap, 1).replace(OLD_MIGRATE, migrate, 1)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('installed_compose', type=Path)
    parser.add_argument('candidate', type=Path, help='new, local path; never a live host path')
    args = parser.parse_args()
    try:
        if args.installed_compose.resolve() == args.candidate.resolve():
            raise ValueError('input and candidate must differ')
        source = args.installed_compose.read_text(encoding='utf-8')
        result = render(source)
        fd = os.open(args.candidate, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w', encoding='utf-8') as output:
            output.write(result)
    except (OSError, ValueError) as exc:
        print(f'compatibility preparation refused: {exc}', file=sys.stderr)
        return 1
    print('local compatibility candidate prepared; no host or credential change')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
