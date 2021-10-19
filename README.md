Aivenator
=========

Provision credentials for Aiven services in the NAIS plattform.

Architecture overview
---------------------

Aivenator has two main components:

### AivenApplication Synchronizer

This component watches AivenApplication objects, and provisions requested credentials and places them in the requested Secret.
It will provision credentials for the requested Aiven services into one secret.
It is the responsibility of the deployment system to mount the secret in the application.

At the end of a reconciliation, it will look for existing secrets that are not in use, and delete them.

Mode of operation: Reconciliation

### Secret Finalizer

When a secret managed by Aivenator is deleted, Kubernetes will first require finalizers to complete.
This component is a finalizer, which makes sure to delete related service users from Aiven.

Mode of operation: Reconciliation

Adding support for new Aiven services
-------------------------------------

When adding support for a new Aiven service, a new package under `pkg/handlers` should be created.
It needs to implement the `pkg/credentials/manager.go::Handler` interface.

When an AivenApplication is synchronized (created, updated or otherwise needs an refresh), the handlers Apply method will be called.
When a Secret managed by Aivenator is finalized, the Cleanup method will be called.

On Apply the handler is given an AivenApplication and a Secret (and a logger).
It should use information in the AivenApplication to make changes to the Secret.
It is important that it should not overwrite or delete information already present in the secret.

On Cleanup the handler is given a Secret (and a logger).
It should use information in the Secret to make necessary cleanup.
This means it is important that any information needed is added as annotations or labels in the Apply method.


Currently supported Aiven services
----------------------------------

- Kafka
- Elastic


Protected Applications
----------------------

Some legacy deployments have a hard time handling AivenApplication objects automatically.
To provide for these setups, an AivenApplication object can be manually created with the `Protected` flag.
A secret managed by Aivenator with the protected flag will not be deleted by the Secret Janitor.
When this feature is used, it is important that the secret is manually deleted when no longer in use.

Working with Aivenator
----------------------

To run locally, Aivenator requires an Aiven API Token.
It should be provided using the `AIVENATOR_AIVEN_TOKEN` environment variable.

It is recommended to debug Aivenator using a local (or on-demand) Kubernetes cluster with the required CRDs loaded.
The CRDs used by Aivenator are defined in [liberator](https://github.com/nais/liberator).

Assuming liberator is checked out in a sibling directory to aivenator, you can use this command to install the AivenApplication CRD in your test cluster:

    kubectl apply -f ../liberator/config/crd/bases/aiven.nais.io_aivenapplications.yaml

In order to run the integration tests, you need to set the `AIVEN_TOKEN` environment variable with a valid Aiven API token.
Some of the integration tests also need the kubebuilder tools.
These will be installed in `./.testbin/` by `make kubebuilder`.
