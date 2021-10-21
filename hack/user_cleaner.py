#!/usr/bin/env python
import argparse
import json
import os
import subprocess
from dataclasses import dataclass

import requests

EXCLUDED_USERS = {
    "avnadmin",
    "kafka-lag-exporter",
}


class AivenAuth(requests.auth.AuthBase):
    def __init__(self, token=None):
        if token is None:
            token = os.getenv("AIVEN_TOKEN")
        self.token = token

    def __call__(self, r):
        r.headers["authorization"] = f"Bearer {self.token}"
        return r


@dataclass
class Acl:
    id: str
    permission: str
    topic: str
    username: str


@dataclass
class Service:
    acls: list[Acl]
    topics: set[str]
    users: set[str]


class AivenKafka(object):
    base = "https://api.aiven.io/v1/project"

    def __init__(self, project, service=None):
        self.project = project
        if service is None:
            service = project + "-kafka"
        self.service = service
        self.session = requests.Session()
        self.session.auth = AivenAuth()
        self.base_url = f"{self.base}/{self.project}/service/{self.service}"

    def get_topics(self):
        resp = self.session.get(f"{self.base_url}/topic")
        resp.raise_for_status()
        data = resp.json()
        return [t["topic_name"] for t in data["topics"]]

    def get_service(self):
        resp = self.session.get(self.base_url)
        resp.raise_for_status()
        data = resp.json()
        acls = [Acl(**a) for a in data["service"]["acl"]]
        topics = {t["topic_name"] for t in data["service"]["topics"]}
        users = {u["username"] for u in data["service"]["users"]}
        return Service(acls, topics, users)

    def get_acls(self):
        resp = self.session.get(f"{self.base_url}/acl")
        resp.raise_for_status()
        data = resp.json()
        return [Acl(**a) for a in data["acl"]]

    def delete_acls(self, acls_to_delete):
        for acl in acls_to_delete:
            print(f"Deleting {acl}")
            resp = self.session.delete(f"{self.base_url}/acl/{acl.id}")
            resp.raise_for_status()

    def delete_users(self, users_to_delete):
        for username in users_to_delete:
            print(f"Deleting {username}")
            resp = self.session.delete(f"{self.base_url}/user/{username}")
            resp.raise_for_status()


def get_users_in_use(context):
    print(f"Loading secrets from {context}")
    cmd = [
        "kubectl",
        "get", "secret",
        "--context", context,
        "--all-namespaces",
        "--selector", "type=aivenator.aiven.nais.io",
        "--output", "json",
    ]
    print(" ".join(cmd))
    output = subprocess.check_output(cmd)
    result = json.loads(output)
    print(f"Found {len(result['items'])} secrets")
    for secret in result["items"]:
        metadata = secret["metadata"]
        annotations = metadata["annotations"]
        user = annotations.get("kafka.aiven.nais.io/serviceUser")
        if user:
            yield user


def main(options):
    aiven_project = f"nav-{options.env}"
    k8s_contexts = {f"{options.env}-{c}" for c in ("gcp", "fss", "sbs")}

    aiven = AivenKafka(aiven_project)
    service = aiven.get_service()
    candidates = service.users
    candidates.difference_update(EXCLUDED_USERS)

    for context in k8s_contexts:
        users_in_use = get_users_in_use(context)
        candidates.difference_update(users_in_use)

    print(f"Want to delete {len(candidates)} users from {aiven_project}")
    for user in list(candidates)[25:]:
        print(user)
    aiven.delete_users(candidates)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument("env", help="Environment to process")
    main(parser.parse_args())
