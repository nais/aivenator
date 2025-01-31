#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = [
#   "requests",
# ]
# ///
import argparse
import base64
import copy
import json
import os
import re
import subprocess
import tempfile
from dataclasses import dataclass

import requests

AIVEN_APP_TEMPLATE = """
apiVersion: aiven.nais.io/v1
kind: AivenApplication
metadata:
  annotations:
    nais.io/migration: "true"
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
"""


class AivenAuth(requests.auth.AuthBase):
    def __init__(self, token=None):
        if token is None:
            token = os.getenv("AIVEN_TOKEN")
        self.token = token

    def __call__(self, r):
        r.headers["authorization"] = f"Bearer {self.token}"
        return r


@dataclass
class MigrationConfig:
    host: str
    port: int
    username: str
    password: str


def create_migration_config(name) -> MigrationConfig:
    if m := re.match(r"redis-(.+?)-(.*)", name):
        namespace = m.group(1)
        instance_name = m.group(2)
    else:
        raise ValueError(f"Invalid name: {name}")

    secret_name = f"aiven-{name}-migration"
    manifest = AIVEN_APP_TEMPLATE.format(
        name=name,
        namespace=namespace,
        secret_name=secret_name,
        instance_name=instance_name,
    )
    print("Applying manifest to create migration user")
    apply_manifest(manifest)
    print("Waiting for secret to be created")
    subprocess.run(["kubectl", "wait", "--timeout=180s", "--for=create", f"secret/{secret_name}"])
    print("Fetching secret")
    output = subprocess.run(["kubectl", "get", "secret", secret_name, "-o", "json"], check=True,
                            capture_output=True).stdout
    secret = json.loads(output)
    data = secret.get("data", {})
    return MigrationConfig(
        host=get_secret_data(data, "host", instance_name),
        port=int(get_secret_data(data, "port", instance_name)),
        username=get_secret_data(data, "username", instance_name),
        password=get_secret_data(data, "password", instance_name),
    )


def apply_manifest(manifest):
    with tempfile.NamedTemporaryFile("w") as f:
        f.write(manifest)
        f.flush()
        subprocess.run(["kubectl", "apply", "-f", f.name], check=True)


def get_secret_data(data, key, instance_name) -> str:
    return base64.b64decode(data[f"REDIS_{key.upper()}_{instance_name.upper()}"]).decode("utf-8")


def allow_terminating_redis(name):
    print("Patching Redis to allow accidental deletion")
    subprocess.run(["kubectl", "patch", "redis.aiven.io", name, "--type=json",
                    "--patch=[{'op': 'replace', 'path': '/spec/terminationProtection', 'value':false}]"], check=True)


def create_replicating_valkey(name, migration_config: MigrationConfig):
    print("Getting existing Redis manifest")
    redis_manifest = json.loads(
        subprocess.run(["kubectl", "get", "redis.aiven.io", name, "-o", "json"], check=True,
                       capture_output=True).stdout)
    valkey_name = make_valkey_name(name)
    valkey_manifest = copy.deepcopy(redis_manifest)
    del valkey_manifest["status"]
    valkey_manifest["kind"] = "Valkey"
    valkey_manifest["metadata"]["name"] = valkey_name
    for metadata_key in (
            "creationTimestamp",
            "finalizers",
            "generation",
            "ownerReferences",
            "resourceVersion",
            "uid",
    ):
        del valkey_manifest["metadata"][metadata_key]
    for annotation in (
            "controllers.aiven.io/generation-was-processed",
            "controllers.aiven.io/instance-is-running",
            "nais.io/deploymentCorrelationID"
    ):
        del valkey_manifest["metadata"]["annotations"][annotation]
    # XXX: Migration like this doesn't work for some reason, so we do that via API later
    # migration = {
    #     "host": migration_config.host,
    #     "port": migration_config.port,
    #     "username": migration_config.username,
    #     "password": migration_config.password,
    #     "ssl": True,
    #     "method": "replication",
    # }
    # if "userConfig" in valkey_manifest["spec"]:
    #     valkey_manifest["spec"]["userConfig"]["migration"] = migration
    # else:
    #     valkey_manifest["spec"]["userConfig"] = {"migration": migration}
    print("Applying Valkey manifest")
    apply_manifest(json.dumps(valkey_manifest))
    print("Waiting for Valkey to be running")
    subprocess.run([
        "kubectl", "wait",
        "--timeout=180s",
        "--for=jsonpath={.status.state}=RUNNING",
        f"valkey/{valkey_name}"
    ], check=True)
    return redis_manifest["spec"]["project"]


def make_valkey_name(name):
    valkey_name = f"valkey-{name[len('redis-'):]}"
    return valkey_name


def start_replication(name, config, project):
    payload = {
        "user_config": {
            "migration": {
                "host": config.host,
                "port": config.port,
                "username": config.username,
                "password": config.password,
                "ssl": True,
                "method": "replication",
            },
        }
    }
    valkey_name = make_valkey_name(name)
    resp = requests.put(f"https://console.aiven.io/v1/project/{project}/service/{valkey_name}", json=payload,
                        auth=AivenAuth())
    resp.raise_for_status()


def main(name):
    print(f"Creating replicating Valkey for {name}")
    print("Create migration config")
    config = create_migration_config(name)
    print("Creating replicating Valkey")
    project = create_replicating_valkey(name, config)
    print("Starting replication")
    start_replication(name, config, project)
    print("Allowing accidental deletion of Redis")
    allow_terminating_redis(name)
    print("All done, remember to update Git")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("name", help="Name of the redis to replicate")
    options = parser.parse_args()
    main(options.name)
