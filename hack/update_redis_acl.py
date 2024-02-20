#!/usr/bin/env python
import argparse

from rich.progress import track

from common import Aiven


def main(project, dry_run):
    aiven = Aiven(project, dry_run)
    services = list(aiven.get_services())
    for service in track(services):
        if service.type == "redis":
            for user in service.users:
                if user.access_control and "-@dangerous" in user.access_control.redis_acl_categories:
                    user.access_control.redis_acl_categories.remove("-@dangerous")
                    aiven.update_service_user_acl(service, user)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument("-n", "--dry-run", action="store_true", help="Make no actual changes")
    parser.add_argument("project", action="store", help="Aiven project to process")
    options = parser.parse_args()
    main(options.project, options.dry_run)
