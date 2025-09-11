let kafka_nais_apps = kubectl get -A aivenapp --no-headers -o json
 | from json
 | get items
 | each {|aivenApp|{
   name: $aivenApp.metadata.name,
   namespace: $aivenApp.metadata.namespace,
   spec: $aivenApp.spec,
 }} | where {|it| 'kafka' in $it.spec}
 let kafka_nais_apps_without_individual_secret = $kafka_nais_apps | where {|it| 'secretName' not-in $it.spec.kafka}
 print $"Total nais apps w/Kafka: ($kafka_nais_apps | length)"
 print $"Nais apps w/Kafka w/o individual secret: ($kafka_nais_apps_without_individual_secret | length)"

$kafka_nais_apps_without_individual_secret | par-each {|app|
    do --ignore-errors {kubectl get -n $app.namespace app $app.name ;
    kubectl patch -n $app.namespace app $app.name --type="merge" --patch '{"status":{"synchronizationHash": ""}}' --dry-run="server"}}