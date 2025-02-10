#!/usr/bin/env bash

for env in dev prod; do
  json_file=$(mktemp --suffix=-${env}.json)
  http --json --auth "${AIVEN_TOKEN}" --auth-type bearer https://api.aiven.io/v1/project/nav-${env}/service > "${json_file}"

  for service in redis valkey; do
    count=$(jq ".services[] | select(.service_type == \"${service}\") | .service_name" < "${json_file}"  | wc -l)
    echo "nav-${env} ${service} ${count}"
  done
done
