#!/usr/bin/bash

if [[ -z "${AIVENATOR_KUBE_CONTEXT}" ]]; then
  echo "You must set 'AIVENATOR_KUBE_CONTEXT' to the context that points to your test-cluster!"
  exit 1
fi

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
CRD_BASE_DIR="${SCRIPT_DIR}/../../liberator/config/crd/bases"

if [[ ! -r "${CRD_BASE_DIR}/*.yaml" ]]; then
  echo "Loading liberator configs into cluster ${AIVENATOR_KUBE_CONTEXT}"
  kubectl apply --filename="${CRD_BASE_DIR}" --context "${AIVENATOR_KUBE_CONTEXT}"
else
  echo "Could not load liberator configs into cluster, because there are no readable files in ${CRD_BASE_DIR}."
  echo "Attempting to continue without them ..."
fi

old_context=$(kubectl config current-context)
kubectl config use-context "${AIVENATOR_KUBE_CONTEXT}"

echo "You are ready to run Aivenator, pausing here until done ..."
read -p "Press any key ..." -N 1 -s
echo

kubectl config use-context "${old_context}"
