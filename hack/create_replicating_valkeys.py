#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# ///
import argparse
import base64
import copy
import json
import subprocess
import tempfile
from dataclasses import dataclass


@dataclass
class MigrationConfig:
    host: str
    port: int
    username: str
    password: str


def create_migration_config(name) -> MigrationConfig:
    print("Fetching secret")
    output = subprocess.run(["kubectl", "get", "secret", name, "-o", "json"], check=True,
                            capture_output=True).stdout
    secret = json.loads(output)
    data = secret.get("data", {})
    return MigrationConfig(
        host=get_secret_data(data, "host"),
        port=int(get_secret_data(data, "port")),
        username=get_secret_data(data, "user"),
        password=get_secret_data(data, "password"),
    )


def apply_manifest(manifest):
    with tempfile.NamedTemporaryFile("w") as f:
        f.write(manifest)
        f.flush()
        subprocess.run(["kubectl", "apply", "-f", f.name], check=True)


def get_secret_data(data, key) -> str:
    env_key = f"REDIS_{key.upper()}".replace("-", "_")
    return base64.b64decode(data[env_key]).decode("utf-8")


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
    migration = {
        "host": migration_config.host,
        "port": migration_config.port,
        "username": migration_config.username,
        "password": migration_config.password,
        "ssl": True,
        "method": "replication",
    }
    if "userConfig" in valkey_manifest["spec"]:
        valkey_manifest["spec"]["userConfig"]["migration"] = migration
    else:
        valkey_manifest["spec"]["userConfig"] = {"migration": migration}
    print("Applying Valkey manifest")
    apply_manifest(json.dumps(valkey_manifest))
    print("Waiting for Valkey to be running")
    subprocess.run([
        "kubectl", "wait",
        "--timeout=300s",
        "--for=jsonpath={.status.state}=RUNNING",
        f"valkey/{valkey_name}"
    ], check=True)
    return redis_manifest["spec"]["project"]


def make_valkey_name(name):
    valkey_name = f"valkey-{name[len('redis-'):]}"
    return valkey_name


def main(name):
    print(f"Creating replicating Valkey for {name}")
    print("Create migration config")
    config = create_migration_config(name)
    print("Creating replicating Valkey")
    create_replicating_valkey(name, config)
    print("Allowing accidental deletion of Redis")
    allow_terminating_redis(name)
    print("All done, remember to update Git")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("name", help="Name of the redis to replicate")
    options = parser.parse_args()
    main(options.name)
