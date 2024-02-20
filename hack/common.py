import os
from typing import Optional

import requests
from pydantic import BaseModel, Field
from rich import get_console


class AivenAuth(requests.auth.AuthBase):
    def __init__(self, token=None):
        if token is None:
            token = os.getenv("AIVEN_TOKEN")
        self.token = token

    def __call__(self, r):
        r.headers["authorization"] = f"Bearer {self.token}"
        return r


class Acl(BaseModel):
    redis_acl_categories: Optional[list[str]] = []
    redis_acl_channels: Optional[list[str]] = []
    redis_acl_commands: Optional[list[str]] = []
    redis_acl_keys: Optional[list[str]] = []


class User(BaseModel):
    username: str
    type: str
    access_control: Optional[Acl] = None


class Service(BaseModel):
    type: str = Field(alias="service_type")
    name: str = Field(alias="service_name")
    users: list[User]


class Aiven(object):
    base = "https://api.aiven.io/v1/project"

    def __init__(self, project, dry_run=False):
        self.project = project
        self.dry_run = dry_run
        self.session = requests.Session()
        self.session.auth = AivenAuth()
        self.base_url = f"{self.base}/{self.project}"

    def get_services(self):
        with get_console().status("Listing services"):
            resp = self.session.get(self.base_url + "/service")
        resp.raise_for_status()
        data = resp.json()
        for service in data["services"]:
            yield Service.parse_obj(service)

    def get_service(self, service):
        with get_console().status("Getting service"):
            resp = self.session.get(self.base_url + f"/service/{service}")
        resp.raise_for_status()
        data = resp.json()
        return Service.parse_obj(data["service"])

    def update_service_user_acl(self, service: Service, user: User):
        print(f"Updating user {user.username} in {service.name}")
        url = f"{self.base_url}/service/{service.name}/user/{user.username}"
        payload = {
            "operation": "set-access-control",
            "access_control": user.access_control.model_dump(),
        }
        if self.dry_run:
            print(f"Would have sent PUT to {url} with payload:\n{payload}")
        else:
            resp = self.session.put(url, json=payload)
            resp.raise_for_status()
