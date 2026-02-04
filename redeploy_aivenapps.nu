#!/usr/bin/env nix-shell
#!nix-shell -i nu -p nushell

let all_aivenapps = kubectl get --all-namespaces aivenapp -o yaml
  | from yaml
  | get items
  | each {|aivenApp|{
   name: $aivenApp.metadata.name,
   namespace: $aivenApp.metadata.namespace,
  }}
print $"Fetched all ($all_aivenapps | length) AivenApps in cluster"
let chunks_of_10_aivenapps = $all_aivenapps
  | chunks 10

mut successes = 0
for chunk in $chunks_of_10_aivenapps {
  print "Sleeping 20s..."
  sleep 20sec # To avoid throttling etcd or the aivenator operator
  for aivenApp in $chunk {
    kubectl patch -n $aivenApp.namespace $"aivenapp/($aivenApp.name)" --type=merge -p '{"status": {"synchronizationHash": ""}}'
    if $env.LAST_EXIT_CODE == 0 {
      $successes = $successes + 1
      print $"\tPatched ($aivenApp.namespace)/($aivenApp.name)\n"
    } else {
      print $"\t\t!!!PATCHING OF ($aivenApp.namespace)/($aivenApp.name) FAILED!!!\n"
    }
  }
  print $"Finished succsessfully w/($successes) AivenApps so far\n\n"
}
