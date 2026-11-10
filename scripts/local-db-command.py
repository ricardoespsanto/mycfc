#!/usr/bin/env python3
"""Run an owned local command with a typed DB identity; never rewrite .env."""
import argparse
import os
import re
import stat
import sys
from urllib.parse import parse_qsl, quote, urlsplit, urlunsplit


class Refused(ValueError):
    pass


def environment(path, inherited):
    if inherited.get('APP_ENV') not in (None, '', 'local', 'test') or inherited.get('TRAINING_SCHEDULE_ADMISSION_FILE'):
        raise Refused('inherited_protected_context')
    if inherited.get('DATABASE_URL'):
        checked_url(inherited['DATABASE_URL'])
    result = dict(inherited)
    # Deliberately a data parser, not a shell or interpolation interpreter.
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o022 or info.st_size > 65536:
            raise Refused('protected_owned_local_env_required')
        data = stream.read(65537)
        if len(data) > 65536:
            raise Refused('local_env_too_large')
    for line in data.decode('utf-8').splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        match = re.fullmatch(r'([A-Z][A-Z0-9_]*)=(.*)', line)
        if not match:
            raise Refused('unsupported_local_env_syntax')
        key, value = match.groups()
        if value.startswith(('"', "'")):
            if len(value) < 2 or value[-1] != value[0]:
                raise Refused('unsupported_local_env_syntax')
            value = value[1:-1]
        # Caller retains control of the Docker daemon/project and CI switches.
        if key.startswith(('COMPOSE_', 'DOCKER_')):
            raise Refused('local_env_targeting_key_refused')
        result[key] = value
    if result.get('APP_ENV') not in ('local', 'test'):
        raise Refused('owned_local_environment_required')
    if result.get('TRAINING_SCHEDULE_ADMISSION_FILE'):
        raise Refused('external_admission_not_local')
    if any(result.get(key) for key in ('PGHOST', 'PGHOSTADDR', 'PGPORT', 'PGDATABASE', 'PGUSER', 'PGPASSWORD',
                                       'PGSERVICE', 'PGSERVICEFILE', 'PGPASSFILE', 'PGOPTIONS')):
        raise Refused('inherited_postgres_configuration_refused')
    for key, value in {'APP_DB_USER': 'mycfc_app', 'APP_DB_PASSWORD': 'mycfc_local_app_only',
                       'MIGRATION_DB_USER': 'mycfc_migration',
                       'MIGRATION_DB_PASSWORD': 'mycfc_local_migration_only'}.items():
        if not result.get(key):
            result[key] = value
    if result.get('DATABASE_URL'):
        port = checked_url(result['DATABASE_URL']).port or 5432
        published = result.get('POSTGRES_PORT', str(port))
        if not re.fullmatch(r'[1-9][0-9]{0,4}', published) or int(published) != port or int(published) > 65535:
            raise Refused('local_published_port_mismatch')
        result['POSTGRES_PORT'] = published
    return result


def checked_url(raw):
    value = urlsplit(raw)
    if value.scheme not in ('postgres', 'postgresql') or value.hostname not in ('localhost', '127.0.0.1', '::1', 'postgres'):
        raise Refused('owned_local_database_required')
    if value.path not in ('/mycfc', '/mycfc_test', '/mycfc_ui_review') or value.fragment or not value.username:
        raise Refused('owned_local_database_required')
    if value.port is not None and not 1 <= value.port <= 65535:
        raise Refused('local_database_port_invalid')
    allowed = {'sslmode', 'sslrootcert', 'sslcert', 'sslkey', 'connect_timeout', 'application_name',
               'ssl_min_protocol_version', 'ssl_max_protocol_version', 'channel_binding'}
    seen = set()
    for key, val in parse_qsl(value.query, keep_blank_values=True, strict_parsing=True):
        # pgx allows query user/host/dbname to override the URI authority.
        # A strict positive list also rejects encoded duplicates/hostaddr/service
        # files and arbitrary PostgreSQL startup options/role/search_path.
        if key not in allowed or key.casefold() in seen or not val:
            raise Refused('local_connection_option_refused')
        seen.add(key.casefold())
        if key == 'sslmode' and val not in ('disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full'):
            raise Refused('local_tls_mode_invalid')
    return value


def identity(env, kind, database=None):
    result = dict(env)
    value = checked_url(result.get('DATABASE_URL', ''))
    if kind != 'runtime' and value.hostname not in ('localhost', '127.0.0.1', '::1'):
        raise Refused('literal_loopback_maintenance_required')
    if any(result.get(key) for key in ('DB_HOST', 'DB_PORT', 'DB_NAME', 'DB_USER', 'DB_PASSWORD')):
        raise Refused('ambiguous_local_database_configuration')
    prefix = {'runtime': 'APP_DB', 'migration': 'MIGRATION_DB', 'bootstrap': 'POSTGRES'}[kind]
    user, password = result.get(prefix+'_USER'), result.get(prefix+'_PASSWORD')
    if not user or not password:
        raise Refused('typed_local_credentials_required')
    authority = value.netloc.rsplit('@', 1)[-1]
    credentials = quote(user, safe='')+':'+quote(password, safe='')+'@'
    result['DATABASE_URL'] = urlunsplit((value.scheme, credentials+authority,
                                      '/'+database if database else value.path, value.query, ''))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('identity', choices=['runtime', 'migration', 'bootstrap'])
    parser.add_argument('--database', choices=['mycfc', 'mycfc_test', 'mycfc_ui_review'])
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    try:
        command = args.command
        if command[:1] == ['--']:
            command = command[1:]
        if not command:
            raise Refused('command_required')
        env = identity(environment('.env', os.environ), args.identity, args.database)
        os.execvpe(command[0], command, env)
    except (OSError, ValueError, KeyError):
        # Exceptions can embed a DSN or password. Emit a fixed diagnostic only.
        print('local_db_command=refused configuration_or_command_invalid', file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
