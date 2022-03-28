#!/usr/bin/env python

"""
Things to do
- Find secrets referencing non-existent application and delete
    - kafka.nais.io/application
- Find secrets not in use and delete
"""
import argparse
import json
import logging
import re
import subprocess
from collections import defaultdict
from dataclasses import dataclass

LOG = logging.getLogger(__name__)

KAFKARATOR_NAME_PATTERN = re.compile(r"kafka-(?P<app>.*)-(?P<pool>nav-.*)-[0-9a-f]{8}")


@dataclass
class ResourceID:
    name: str
    namespace: str
    context: str

    def __str__(self):
        return f"{self.name}/{self.namespace} in {self.context}"

    @classmethod
    def from_object(cls, obj, context):
        metadata = obj["metadata"]
        return cls(metadata["name"], metadata["namespace"], context)


def list_pods(contexts):
    for context in contexts:
        LOG.info(f"Loading Pods from {context}")
        cmd = (
            "kubectl",
            "get", "pods",
            "--context", context,
            "--all-namespaces",
            "--output", "json",
        )
        LOG.debug(" ".join(cmd))
        output = subprocess.run(cmd, check=True, stdout=subprocess.PIPE, encoding="utf-8").stdout
        result = json.loads(output)
        LOG.info(f"Found {len(result['items'])} Pods")
        yield from ((context, pod) for pod in result["items"])


def list_secrets(contexts):
    for context in contexts:
        LOG.info(f"Loading Secrets from {context}")
        cmd = (
            "kubectl",
            "get", "secret",
            "--context", context,
            "--all-namespaces",
            "--output", "json",
        )
        LOG.debug(" ".join(cmd))
        output = subprocess.run(cmd, check=True, stdout=subprocess.PIPE, encoding="utf-8").stdout
        result = json.loads(output)
        LOG.info(f"Found {len(result['items'])} secrets")
        for secret in result["items"]:
            metadata = secret["metadata"]
            secret_name = metadata["name"]
            namespace = metadata["namespace"]
            # Make sure annotations and labels exists in metadata
            metadata.setdefault("labels", {})
            metadata.setdefault("annotations", {})
            if KAFKARATOR_NAME_PATTERN.match(secret_name):
                yield secret, ResourceID(secret_name, namespace, context)


def delete(secret_id):
    LOG.info("Deleting secret %s", secret_id)
    cmd = (
        "kubectl",
        "delete", "secret",
        "--context", secret_id.context,
        "--namespace", secret_id.namespace,
        secret_id.name,
    )
    LOG.debug(" ".join(cmd))
    subprocess.run(cmd, check=True)


def get_all_containers(pod_spec):
    for container in pod_spec.get("containers", []):
        yield container
    for container in pod_spec.get("initContainers", []):
        yield container


def get_volumes(pod_spec):
    for volume in pod_spec.get("volumes", []):
        yield volume


def used_by_env_from(secret_id, container):
    for env_from_source in container.get("envFrom", []):
        secret_env_source = env_from_source.get("secretRef")
        if secret_env_source:
            if secret_env_source["name"] == secret_id.name:
                return True
    return False


def used_by_env(secret_id, container):
    for env_var in container.get("env", []):
        env_var_source = env_var.get("valueFrom")
        if env_var_source:
            secret_key_selector = env_var_source.get("secretKeyRef")
            if secret_key_selector:
                if secret_key_selector["name"] == secret_id.name:
                    return True
    return False


def used_by_volume(secret_id, volume):
    secret_volume_source = volume.get("secret")
    if secret_volume_source:
        if secret_volume_source["secretName"] == secret_id.name:
            return True
    return False


def in_use(secret_id, pods):
    """Figure out if secret is in use. We only care about use by workloads (pods).

    We will check for references in ReplicaSets, since we also want to detect use in scaled down deployments

    Things to check for:
    - .spec.containers[*].env[*].valueFrom.secretKeyRef.name
    - .spec.initContainers[*].env[*].valueFrom.secretKeyRef.name
    - .spec.containers[*].envFrom[*].secretRef.name
    - .spec.initContainers[*].envFrom[*].secretRef.name
    - .spec.volumes[*].secret.secretName
    """
    for context, pod in pods:
        pod_id = ResourceID.from_object(pod, context)
        pod_spec = pod["spec"]
        for container in get_all_containers(pod_spec):
            if used_by_env_from(secret_id, container):
                LOG.debug("%s is in use by envFrom in pod %s", secret_id, pod_id)
                return True
            if used_by_env(secret_id, container):
                LOG.debug("%s is in use by env in pod %s", secret_id, pod_id)
                return True
        for volume in get_volumes(pod_spec):
            if used_by_volume(secret_id, volume):
                LOG.debug("%s is in use by volume in pod %s", secret_id, pod_id)
                return True
    return False


def main(options):
    k8s_contexts = [f"{options.env}-{c}" for c in ("gcp", "fss", "sbs")]

    total_count = 0
    deleted_count = 0
    pods = list_pods(k8s_contexts)
    pods_by_namespace = defaultdict(list)
    for context, pod in pods:
        pods_by_namespace[pod["metadata"]["namespace"]].append((context, pod))
    for secret, secret_id in list_secrets(k8s_contexts):
        total_count += 1
        metadata = secret["metadata"]
        namespace = metadata["namespace"]
        if not in_use(secret_id, pods_by_namespace[namespace]):
            try:
                delete(secret_id)
                deleted_count += 1
            except subprocess.CalledProcessError as e:
                LOG.error("Failed to delete secret %s: %s", secret_id, e.output)
    LOG.info("Deleted %d of %d secrets", deleted_count, total_count)


if __name__ == '__main__':
    logging.basicConfig(format="[%(asctime)s|%(levelname)5.5s] %(message)s", level=logging.DEBUG)
    parser = argparse.ArgumentParser()
    parser.add_argument("env", help="Environment to process")
    main(parser.parse_args())
