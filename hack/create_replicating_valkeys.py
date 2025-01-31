#!/usr/bin/env python
import argparse
import base64
import copy
import json
import re
import subprocess
import tempfile
from dataclasses import dataclass

AIVEN_APP_TEMPLATE = '''
apiVersion: aiven.nais.io/v1
kind: AivenApplication
metadata:
  annotations:
    nais.io/migration: 'true'
  labels:
    app: {name}
    team: {namespace}
  name: valkey-migration-{name}
  namespace: {namespace}
spec:
  secretName: {secret_name}
  redis:
  - access: admin
    instance: {instance_name}
'''


@dataclass
class MigrationConfig:
    host: str
    port: int
    username: str
    password: str


def create_migration_config(name) -> MigrationConfig:
    if m := re.match(r'redis-(.+?)-(.*)', name):
        namespace = m.group(1)
        instance_name = m.group(2)
    else:
        raise ValueError(f'Invalid name: {name}')

    secret_name = f'aiven-{name}-migration'
    manifest = AIVEN_APP_TEMPLATE.format(
        name=name,
        namespace=namespace,
        secret_name=secret_name,
        instance_name=instance_name,
    )
    print('Applying manifest to create migration config')
    apply_manifest(manifest)
    print('Waiting for secret to be created')
    subprocess.run(['kubectl', 'wait', '--timeout=180s', '--for=create', f'secret/{secret_name}'])
    print('Fetching secret')
    output = subprocess.run(['kubectl', 'get', 'secret', secret_name, '-o', 'json'], check=True,
                            capture_output=True).stdout
    secret = json.loads(output)
    data = secret.get('data', {})
    return MigrationConfig(
        host=get_secret_data(data, 'host', instance_name),
        port=int(get_secret_data(data, 'port', instance_name)),
        username=get_secret_data(data, 'username', instance_name),
        password=get_secret_data(data, 'password', instance_name),
    )


def apply_manifest(manifest):
    with tempfile.NamedTemporaryFile('w') as f:
        f.write(manifest)
        f.flush()
        cmd = (
            'kubectl',
            'apply',
            '-f', f.name,
        )
        subprocess.run(cmd, check=True)


def get_secret_data(data, key, instance_name) -> str:
    return base64.b64decode(data[f'REDIS_{key.upper()}_{instance_name.upper()}']).decode('utf-8')


def allow_terminating_redis(name):
    print('Patching Redis to allow accidental deletion')
    subprocess.run(['kubectl', 'patch', 'redis.aiven.io', name, '--type=json',
                    "--patch=[{'op': 'replace', 'path': '/spec/terminationProtection', 'value':false}]"], check=True)


def create_replicating_valkey(name, migration_config: MigrationConfig):
    print('Getting existing Redis manifest')
    redis_manifest = json.loads(
        subprocess.run(['kubectl', 'get', 'redis.aiven.io', name, '-o', 'json'], check=True,
                       capture_output=True).stdout)
    valkey_manifest = copy.deepcopy(redis_manifest)
    valkey_manifest['kind'] = 'Valkey'
    valkey_manifest['metadata']['name'] = f"valkey-{name[len('redis-'):]}"
    migration = {
        'host': migration_config.host,
        'port': migration_config.port,
        'username': migration_config.username,
        'password': migration_config.password,
        'ssl': True,
        'method': 'replication',
    }
    if 'userConfig' in valkey_manifest['spec']:
        valkey_manifest['spec']['userConfig']['migration'] = migration
    else:
        valkey_manifest['spec']['userConfig'] = {'migration': migration}
    print('Applying Valkey manifest')
    apply_manifest(json.dumps(valkey_manifest))


def main(name):
    print(f'Creating replicating Valkey for {name}')
    print('Create migration config')
    config = create_migration_config(name)
    print('Creating replicating Valkey')
    create_replicating_valkey(name, config)
    print('Allowing accidental deletion of Redis')
    allow_terminating_redis(name)
    print('All done, remember to update Git')


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('name', help='Name of the redis to replicate')
    options = parser.parse_args()
    main(options.name)
