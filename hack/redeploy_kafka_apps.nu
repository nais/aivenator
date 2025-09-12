#!/usr/bin/env nix-shell
#!nix-shell -i nu -p nushell

let kafka_aivenapps = kubectl get -A aivenapp --no-headers -o json
 | from json
 | get items
 | each {|aivenApp|{
   name: $aivenApp.metadata.name,
   namespace: $aivenApp.metadata.namespace,
   spec: $aivenApp.spec,
 }} | where {|it| 'kafka' in $it.spec}

let kafka_nais_apps_without_individual_secret = $kafka_aivenapps | where {|it| 'secretName' not-in $it.spec.kafka}
print $"Total aivenapps w/Kafka: ($kafka_aivenapps | length)"
print $"Aivenapps w/Kafka w/o individual secret: ($kafka_nais_apps_without_individual_secret | length)"

let kafka_aivenapps_without_nais_app = $kafka_aivenapps | where {|app|
  do --ignore-errors {
    kubectl get -n $app.namespace application.nais.io $app.name
  }
}
print $"Aivenapps w/kafka w/o nais app: ($kafka_aivenapps_without_nais_app)"

print $"\nContinue w/patching of aivenapps w/nais app? [Y/n]: "
let user_input = input
if $user_input !~ "(?i)y?" {
  exit 1
};

$kafka_nais_apps_without_individual_secret | each {|app|
  do --ignore-errors {
    kubectl get -n $app.namespace application.nais.io $app.name;
    if $env.LAST_EXIT_CODE != 0 {
      return
    }
    kubectl patch -n $app.namespace app $app.name --type="merge" --patch '{"status":{"synchronizationHash": ""}}' # --dry-run="server"
  }
}
