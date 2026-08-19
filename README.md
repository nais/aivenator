# Aivenator

Provision credentials for Aiven services in the NAIS platform.

Aivenator reacts to AivenApplication objects and decides whether their credentials need synchronization.
It creates or reuses service users in Aiven and writes their credentials to the requested Kubernetes Secrets.
It also removes unused Secrets not in use and cleans up related Aiven service users when those Secrets are deleted.

## Architecture overview

Aivenator has three main components:

### AivenApplication Synchronizer

This component watches AivenApplication objects, and provisions requested Aiven service user credentials and places them in the requested Secrets.
It will provision credentials for the requested Aiven services into one or more secrets. # wtf
It is NOT the responsibility of Aivenator to mount the secret in the application.

Mode of operation: Reconciliation

### Secret Janitor # wtf? How is this different from finalizer??

When an AivenApplication is reconciled, this component looks for existing managed secrets that are not in use, and deletes them.
It also checks all managed secrets every 15 minutes.

Mode of operation: Reconciliation and periodic cleanup

### Secret Finalizer

When a secret managed by Aivenator is deleted, Kubernetes will first require finalizers to complete.
This component is a finalizer, which makes sure to delete related service users and OpenSearch ACL entries from Aiven.

Mode of operation: Reconciliation

### Adding support for new Aiven services

When adding support for a new Aiven service, a new package under `pkg/services` should be created.
It needs to implement the `pkg/credentials/manager.go::ServiceHandler` interface.

When an AivenApplication needs synchronization, the handlers Apply method will be called.
When a Secret managed by Aivenator is finalized, the Cleanup method will be called.

On Apply the handler is given an AivenApplication (and a logger), and returns generated Secrets.
It should use information in the AivenApplication to create complete Secrets.
Returned Secrets replace existing Secrets with the same names.

On Cleanup the handler is given a Secret (and a logger).
It should use information in the Secret to make necessary cleanup.
This means it is important that any information needed is added as annotations or labels in the Apply method.

## Caveat emptor

### Externally
- The underlying Aiven service must already exist.
  OpenSearch and Valkey also require a corresponding k8s resource in the same namespace with a `RUNNING` state.
- Changes made by other automation to Aivenator-managed Secrets are overwritten on the next synchronization.
- Changes to generated Secrets do not trigger AivenApplication synchronization.
- The Kafka service-user naming scheme is:
  - shared with github.com/nais/kafkarator
  - assumed by github.com/nais/aiven-poke

### Internally
- Aivenator reconciliations are not atomic; a failed reconciliation may leave changes in Aiven without updating the corresponding Secrets.
- Deleting an AivenApplication does not directly delete its Secrets; the Secret Janitor later evaluates them for deletion.
- The Helm chart runs one replica, and the controller does not configure leader election.
- OpenSearch supports both current and legacy service naming conventions.


## Currently supported Aiven services

- Kafka
- OpenSearch
- Valkey

## Protected Applications

Some legacy deployments have a hard time handling AivenApplication objects automatically.
To provide for these setups, an AivenApplication object can be manually created with the `Protected` flag.
The Secret Janitor retains a protected secret unless it has an expired time limit and is otherwise unused.
When this feature is used without a time limit, it is important that the secret is manually deleted when no longer in use.

## Working with Aivenator

To run locally, Aivenator requires an Aiven API Token.
It should be provided using the `AIVENATOR_AIVEN_TOKEN` environment variable.

It is recommended to debug Aivenator using a local (or on-demand) Kubernetes cluster with the required CRDs loaded.
The CRDs used by Aivenator are defined in [liberator](https://github.com/nais/liberator).

Assuming liberator is checked out in a sibling directory to aivenator, you can use this command to install the AivenApplication CRD in your test cluster:

    kubectl apply -f ../liberator/config/crd/bases/aiven.nais.io_aivenapplications.yaml

In order to run the integration tests, you need to set the `AIVEN_TOKEN` environment variable with a valid Aiven API token.
The integration tests also need envtest assets.
The `mise run test:integration` task obtains these assets and sets `KUBEBUILDER_ASSETS`.
The tests use the `dev-nais-dev` project and create and delete real Aiven service users.

## Verifying the Aivenator image and its contents

The image is signed "keylessly" (is that a word?) using [Sigstore cosign](https://github.com/sigstore/cosign).
To verify its authenticity run
```
cosign verify europe-north1-docker.pkg.dev/nais-io/nais/images/aivenator:<tag> \
--certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
--certificate-identity "https://github.com/nais/aivenator/.github/workflows/main.yml@refs/heads/main"
```

The images are also attested with SBOMs in the [CycloneDX](https://cyclonedx.org/) format.
You can verify these by running
```
cosign verify-attestation --type cyclonedx \
--certificate-identity "https://github.com/nais/aivenator/.github/workflows/main.yml@refs/heads/main" \
--certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
europe-north1-docker.pkg.dev/nais-io/nais/images/aivenator@sha256:<shasum>
```
