#!/usr/bin/env nix-shell
#!nix-shell -i nu -p nushell

print "Fetching aivenapps & naisapps from k8s..."
let kafka_aivenapps_with_kafk_spec = kubectl get -A aivenapp --no-headers -o json
  | from json
  | get items
  | each {|aivenApp|{
   name: $aivenApp.metadata.name,
   namespace: $aivenApp.metadata.namespace,
   spec: $aivenApp.spec,
  }}
  | where {|it| 'kafka' in $it.spec}
print $"Fetched all ($kafka_aivenapps_with_kafk_spec | length) AivenApps w/Kafka"

let naisapps_with_matching_aivenapp = kubectl get -A application.nais.io --no-headers -o json
  | from json
  | get items
  | each {|naisApp| {
      name: $naisApp.metadata.name,
      namespace: $naisApp.metadata.namespace,
    }
  }
  | where {|naisApp|
    $kafka_aivenapps_with_kafk_spec | any {|aivenApp|
      $aivenApp.name == $naisApp.name and $aivenApp.namespace == $naisApp.namespace
    }
  }
print $"Fetched all ($naisapps_with_matching_aivenapp | length) Nais apps that we have found matching \(name & namespace) AivenApps w/Kafka\n"

let aivenapps_missing_kafka_individual_secret = $kafka_aivenapps_with_kafk_spec | where {|it| 'secretName' not-in $it.spec.kafka}
print $"Aivenapps w/Kafka w/o individual secret: ($aivenapps_missing_kafka_individual_secret | length)"

let aivenapps_missing_matching_naisapp = $aivenapps_missing_kafka_individual_secret
  | where {|aivenApp|
    $naisapps_with_matching_aivenapp | all {|naisApp|
      not ($aivenApp.name == $naisApp.name and $aivenApp.namespace == $naisApp.namespace)
    }
  }
print $"Aivenapps w/kafka w/o nais app: ($aivenapps_missing_matching_naisapp | length)\n"
let aivenapps_with_naisapp = $aivenapps_missing_kafka_individual_secret
  | where {|aivenApp|
    $naisapps_with_matching_aivenapp | any {|naisApp|
      $aivenApp.name == $naisApp.name and $aivenApp.namespace == $naisApp.namespace
    }
  }

match (
  input $"Patch all ($aivenapps_with_naisapp | length) aivenapps that have nais app? [Y/n]:"
    | split chars
    | get -o 0
    | default ""
    | str downcase
) {
  "" | "y" => {print "Continuing...\n"},
  _ => {
    print "Exiting"
    exit 1
  },
}
let patched_kafka_nais_apps = $aivenapps_with_naisapp | each {|app|
  do --ignore-errors {
    print $"Patching nais app ($app.namespace)/($app.name)"
    kubectl get -n $app.namespace application.nais.io $app.name out> /dev/null
    if $env.LAST_EXIT_CODE != 0 {
      return
    }
  }
  kubectl patch -n $app.namespace app $app.name --type="merge" --patch '{"status":{"synchronizationHash": ""}}' # --dry-run="server"
}
print $"Patched nais apps w/aiven apps that use kafka shared secret: ($patched_kafka_nais_apps)"
$patched_kafka_nais_apps
