#!/usr/bin/env python

"""
Things to do
- Find secrets missing metadata
    - Name starts with `kafka`, ends with pool and 8 char hex string
    - Add finalizer
    - Add ServiceUser Annotation: kafka.aiven.nais.io/serviceUser (copy from KAFKA_SCHEMA_REGISTRY_USER data if available)
    - Add Pool Annotation: kafka.aiven.nais.io/pool (copy from kafka.nais.io/pool annotation if available)
    - Add type label: aivenator.aiven.nais.io
    - Add app label: app (copy from kafka.nais.io/application annotation if available)
    - List secrets that failed processing
"""
import argparse
import base64
import copy
import json
import logging
import re
import subprocess
from pprint import pformat

SERVICE_USER_DATA_KEY = "KAFKA_SCHEMA_REGISTRY_USER"
SERVICE_USER_ANNOTATION = "kafka.aiven.nais.io/serviceUser"
POOL_ANNOTATION = "kafka.aiven.nais.io/pool"
OLD_POOL_ANNOTATION = "kafka.nais.io/pool"
APPLICATION_LABEL = "app"
APPLICATION_ANNOTATION = "kafka.nais.io/application"
SECRET_TYPE_LABEL = "type"
SECRET_TYPE_LABEL_VALUE = "aivenator.aiven.nais.io"

LOG = logging.getLogger(__name__)
NAME_PATTERN = re.compile(r"kafka-(?P<app>.*)-(?P<pool>nav-.*)-[0-9a-f]{8}")


class MissingMetadata(Exception):
    pass


def list_secrets(context):
    LOG.info(f"Loading secrets from {context}")
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
        if NAME_PATTERN.match(secret_name):
            # Make sure annotations and labels exists in metadata
            metadata.setdefault("labels", {})
            metadata.setdefault("annotations", {})
            yield secret


def get_app_name(metadata, app_name):
    app_name = metadata["annotations"].get(APPLICATION_ANNOTATION, app_name)
    app_name = metadata["labels"].get(APPLICATION_LABEL, app_name)
    return app_name


def get_pool(metadata, pool):
    annotations = metadata["annotations"]
    pool = annotations.get(OLD_POOL_ANNOTATION, pool)
    pool = annotations.get(POOL_ANNOTATION, pool)
    return pool


def get_service_user(secret):
    annotations = secret["metadata"]["annotations"]
    service_user = annotations.get(SERVICE_USER_ANNOTATION)
    if service_user:
        return service_user
    registry_user_b64 = secret["data"].get(SERVICE_USER_DATA_KEY)
    if registry_user_b64:
        registry_user = base64.b64decode(registry_user_b64).decode("utf-8")
        if registry_user:
            return registry_user
    raise MissingMetadata("Unable to determine service user")


def update_secret(context, secret, app, pool, service_user):
    """
    - Add finalizer
    - Add ServiceUser Annotation: kafka.aiven.nais.io/serviceUser (copy from KAFKA_SCHEMA_REGISTRY_USER data if available)
    - Add Pool Annotation: kafka.aiven.nais.io/pool (copy from kafka.nais.io/pool annotation if available)
    - Add type label: aivenator.aiven.nais.io
    - Add app label: app (copy from kafka.nais.io/application annotation if available)
    """
    new_secret = copy.deepcopy(secret)
    metadata = new_secret["metadata"]
    name = metadata["name"]
    namespace = metadata["namespace"]

    annotations = metadata["annotations"]
    annotations[SERVICE_USER_ANNOTATION] = service_user
    annotations[POOL_ANNOTATION] = pool
    labels = metadata["labels"]
    labels[SECRET_TYPE_LABEL] = SECRET_TYPE_LABEL_VALUE
    labels[APPLICATION_LABEL] = app

    if secret != new_secret:
        LOG.info("Modifying secret %s/%s", name, namespace)
        LOG.debug("Untouched secret metadata:\n%s", pformat(secret["metadata"]))
        LOG.debug("Modified secret metadata:\n%s", pformat(new_secret["metadata"]))
        cmd = (
            "kubectl",
            "apply",
            "--context", context,
            "-f", "-",
        )
        LOG.debug(" ".join(cmd))
        subprocess.run(cmd, check=True, encoding="utf-8", input=json.dumps(new_secret))
        return 1
    LOG.debug("No changes to secret %s/%s", name, namespace)
    return 0


def process(secrets, context):
    count = 0
    for secret in secrets:
        metadata = secret["metadata"]
        secret_name = metadata["name"]
        namespace = metadata["namespace"]
        m = NAME_PATTERN.match(secret_name)
        if not m:
            LOG.warning("Secret %s/%s matched when listed, but not during processing!", secret_name, namespace)
            continue
        try:
            app = get_app_name(metadata, m.group("app"))
            pool = get_pool(metadata, m.group("pool"))
            service_user = get_service_user(secret)
            count += update_secret(context, secret, app, pool, service_user)
        except MissingMetadata as e:
            LOG.warning("Secret %s/%s was missing metadata: %s", secret_name, namespace, e)
            yield secret
        except subprocess.CalledProcessError as e:
            LOG.error("Secret %s/%s could not be updated: %s", secret_name, namespace, e.output)
            yield secret
    LOG.info("Modified %d secrets in %s", count, context)


def report_failed(all_failed):
    LOG.warning("%s secrets failed processing", len(all_failed))
    for context, secret in all_failed:
        metadata = secret["metadata"]
        name = metadata["name"]
        namespace = metadata["namespace"]
        LOG.warning("%s/%s in %s failed", name, namespace, context)


def main(options):
    k8s_contexts = {f"{options.env}-{c}" for c in ("gcp", "fss", "sbs")}

    all_failed = []
    for context in k8s_contexts:
        secrets = list_secrets(context)
        failed = process(secrets, context)
        all_failed.extend((context, secret) for secret in failed)
    report_failed(all_failed)


if __name__ == '__main__':
    logging.basicConfig(format="[%(asctime)s|%(levelname)5.5s] %(message)s", level=logging.DEBUG)
    parser = argparse.ArgumentParser()
    parser.add_argument("env", help="Environment to process")
    main(parser.parse_args())
