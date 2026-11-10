# 1. Aiven user creation today, and migration to the official aiven-operator

Date: 2026-10-29

## Context

Aivenator creates Aiven *service users* and credential `Secret`s for three
services: **Kafka**, **Valkey**, and **OpenSearch**. This ADR documents how that
works today across the full Nais pipeline, then analyses what it would take to
replace the user-creation machinery with the official
[aiven-operator](https://github.com/aiven/aiven-operator).

### The pipeline today

```
nais.io/Application  (developer manifest)
        │
        ▼
   naiserator ──────────► creates AivenApplication  (+ Stream for Kafka)
        │                        │
        │                        ▼
        │                   aivenator ──► creates Aiven service users + writes Secret
        │                                  (Kafka, Valkey, OpenSearch)
        ▼
   kafkarator ──► creates Kafka Topics + ACLs  (wildcard usernames)
```

**liberator** underpins all of them as a shared Go library: it defines the CRD
types (`AivenApplication`, `Topic`, `Stream`) and the deterministic naming
helpers that let the decoupled operators coordinate.

### Component responsibilities

| Component  | Role                                                                                                                                                                                   | Creates users?      |
|------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------|
| naiserator | Front of pipeline. Translates `Application` → `AivenApplication` (`spec.kafka`/`openSearch`/`valkey`) + `Stream`. Injects env vars into the pod.                                       | No                  |
| aivenator  | Reconciles `AivenApplication`. Creates the Aiven service users for all three services and writes credential `Secret`s.                                                                 | **Yes — all three** |
| kafkarator | Reconciles `Topic`/`Stream`. Manages only Kafka topics and ACLs, using wildcard usernames `{team}_{app}_{hash}_*`. Used to create users; split out to aivenator (kafkarator ADR-0005). | No (topics)         |
| liberator  | Shared CRD types + naming helpers. Single source of truth for the username convention.                                                                                                 | No (library)        |

### How aivenator creates each user

All three services go through a single `ServiceHandler` interface
(`pkg/credentials/manager.go`) and a shared `ServiceUserManager`
(`pkg/aiven/serviceuser/serviceuser.go`), which calls the Aiven API via
`github.com/aiven/aiven-go-client/v2`. Each handler:

1. Validates the spec section and resolves the Aiven service name
   (Valkey/OpenSearch additionally require the underlying `aiven.io/v1alpha1`
   CR to be in state `RUNNING`, checked via `pkg/utils/instance_validation.go`).
2. Derives a deterministic service-user name (reusing an existing one if the
   Secret already carries the annotation).
3. Creates/updates the Aiven service user.
4. Fills a `corev1.Secret` with connection info + credentials, stamps
   annotations (e.g. `kafka.aiven.nais.io/serviceUser`) and the finalizer
   `aivenator.aiven.nais.io/finalizer`.

Service-specific details:

- **Kafka** (`pkg/services/kafka/kafka.go`): service user created with **no
  access control** — authorization is external, provided by kafkarator's
  wildcard ACLs that match the deterministic prefix. Credentials are
  **mutual-TLS certs** (`AccessCert`/`AccessKey`) and are additionally packed
  into PKCS12 `client.keystore.p12` / `client.truststore.jks` (via
  `pkg/certificate/native.go`). Secret keys: `KAFKA_CERTIFICATE`,
  `KAFKA_PRIVATE_KEY`, `KAFKA_BROKERS`, `KAFKA_SCHEMA_REGISTRY*`, `KAFKA_CA`,
  `KAFKA_CREDSTORE_PASSWORD`, …
- **Valkey** (`pkg/services/valkey/valkey.go`): supports multiple instances;
  **password auth + inline Valkey ACLs** derived from the `access` level.
  Secret keys: `VALKEY_*` and legacy `REDIS_*` aliases, suffixed per instance.
- **OpenSearch** (`pkg/services/opensearch/opensearch.go`): single instance;
  **password auth + a separate OpenSearch ACL** (via the ACL API). Secret
  keys: `OPEN_SEARCH_*`.

Cleanup is driven by a Secret **finalizer** (delete the Aiven user + ACL) and a
**janitor** that garbage-collects unused/expired managed Secrets. The
`AivenApplication` spec also supports `protected` and `expiresAt`.

### The Postgres precedent (how this migration is shaped)

pgrator already does exactly this for Postgres, and the plan is to mirror it. Two
layers of CRD are involved:

- **Instance CRD** — `nais.io/v1 Valkey` and `nais.io/v1 OpenSearch` already exist
  in pgrator (tier/memory/version etc.), analogous to the Postgres instance CRD.
  Their reconcilers turn them into the official `aiven.io/v1alpha1` service CRs
  (`aiven_v1alpha1.Valkey` + a `ServiceIntegration`). This layer is **done** —
  it is not what this migration adds.
- **Binding CRD** — `nais.io/v1 PostgresBinding` is the **per-workload access
  request**. Naiserator creates one per workload (name `<instance>-<app>`, with a
  `Consumer.Workload`, a `Credentials []` list, and a `SecretName` it picks), and
  pgrator reconciles it into database roles/users plus the connection `Secret`.
  Deleted when the workload stops using it. **No Valkey/OpenSearch binding exists
  yet — that is the new work.**

pgrator reconcilers return `[]action.Action` (never mutate k8s directly), use a
generic synchronizer, declare `OwnedTypes()`, and emit `aiven_v1alpha1.*` child
objects. The official aiven-operator `ServiceUser` becomes a new owned type.
pgrator also already has an env-var-prefix precedent in `ConnectionEnvPrefix`.

### Target architecture

```
nais.io/Application  (spec.valkey / spec.openSearch / spec.kafka + access level)
        │
        ▼
   naiserator ──► creates per-workload binding CRDs:  nais.io/v1 ValkeyBinding,
        │          OpenSearchBinding, KafkaBinding  (name <instance>-<app>, SecretName)
        │          + sets env vars on the pod referencing the Secret
        ▼
   pgrator ──► new reconcilers turn each *Binding into official aiven-operator
        │       ServiceUser (+ ACL / OpenSearchACLConfig) CRs
        ▼
   aiven-operator ──► creates the Aiven service user + writes the connection Secret
```

**aivenator is retired.** Its Aiven API integration
(`pkg/aiven/serviceuser`, the per-service handlers, janitor, finalizer) goes
away. The instance CRDs already exist in pgrator; only the three binding CRDs and
their reconcilers are new.

### How the official aiven-operator does it

The official operator is CRD-driven (`aiven.io/v1alpha1`), not
`AivenApplication`-driven:

- **One generic `ServiceUser` CRD** covers Kafka/Valkey/OpenSearch (no
  per-service user kind). It references its parent service via `project` +
  `serviceName`, and writes a **prefixed** `Secret` owned by the CR
  (`SERVICEUSER_USERNAME`, `_PASSWORD`, `_ACCESS_CERT`, `_ACCESS_KEY`,
  `_CA_CERT`, and for Kafka `_SASL_*` / `_SCHEMA_REGISTRY_*`).
- **Aiven generates the password by default**; managing the password yourself
  is opt-in via `connInfoSecretSource`.
- **ACLs are separate CRDs**: `KafkaACL` / `KafkaNativeACL` /
  `KafkaSchemaRegistryACL`, and `OpenSearchACLConfig`. Valkey ACLs are inline
  on `ServiceUser.spec.accessControl`.
- Secret **rotation** is not built into `ServiceUser`; a separate
  `ServiceUserRotation` CRD rotates between two pre-existing users.
- The client library is `github.com/aiven/go-client-codegen` (the generated
  Aiven API client).

### Gap analysis

In the target, pgrator owns the translation from binding CRD to aiven-operator
CRs, so the gaps below are mostly pgrator's responsibility (shaping the child CRs
and the resulting `Secret`), with one naiserator-side piece for env vars:

1. **PKCS12 credstores (pgrator).** aivenator builds `client.keystore.p12` /
   `client.truststore.jks` for Kafka; the operator only emits raw certs. The
   KafkaBinding reconciler must still assemble the Java stores into the Secret.
2. **Secret key names + env vars (pgrator + naiserator).** Apps expect
   `KAFKA_*` / `VALKEY_*` / `OPEN_SEARCH_*` keys; the operator emits
   `SERVICEUSER_*` (or a configured `connInfoSecretTarget.prefix`). pgrator must
   shape the Secret to the expected keys, and naiserator must inject the matching
   env vars (mirroring `ConnectionEnvPrefix`). **The legacy `REDIS_*` aliases are
   dropped as part of this migration** (to be revisited separately).
3. **Password ownership & rotation (pgrator).** aivenator owns/generates
   credentials and rotates per deploy (overlapping validity); the operator defers
   to Aiven and uses `ServiceUserRotation` for rotation. The deploy-time rotation
   model must be reconstructed, or consciously changed.
4. **Secret lifecycle (pgrator).** aivenator has a janitor, finalizer, and
   `protected`/`expiresAt` semantics. In the target the Secret is owned by the
   binding's child CRs and cleaned up when the binding is deleted (as with
   `PostgresBinding`). `protected`/`expiresAt` has no direct equivalent and needs
   an explicit replacement or a conscious drop.
5. **Naming convention (pgrator, Kafka only).** kafkarator's wildcard ACLs match
   `{team}_{app}_{hash}_{suffix}` (liberator's `ServiceUserNameWithSuffix`). The
   operator defaults the username to the CR name, so the KafkaBinding reconciler
   must set `ServiceUser.spec.username` to the deterministic name, or kafkarator's
   ACL matching changes too.
6. **Who emits the binding CRs (naiserator).** naiserator creates
   `ValkeyBinding` / `OpenSearchBinding` / `KafkaBinding` per workload (name
   `<instance>-<app>`, chosen `SecretName`) instead of `AivenApplication`, and
   deletes them when access is dropped — exactly as it does `PostgresBinding`.

## Decision

Mirror the Postgres model in pgrator, retiring aivenator and moving user/secret
creation to the official aiven-operator. Because Kafka is the hardest part
(PKCS12 credstores, cert-based auth, and the deterministic username kafkarator's
wildcard ACLs depend on), do this in **two phases**.

### Phase 1 — Valkey + OpenSearch

- **Two new binding CRDs** in `nais.io/v1`: `ValkeyBinding` and
  `OpenSearchBinding`, each a per-workload access request modelled on
  `PostgresBinding` (`Consumer.Workload`, access/credentials, `SecretName`).
- **pgrator reconcilers** turn them into aiven-operator `ServiceUser` CRs —
  Valkey ACLs inline on `ServiceUser.spec.accessControl`, OpenSearch ACLs via
  `OpenSearchACLConfig` — and shape the resulting `Secret` to the keys apps
  expect. Reuses the instance CRDs (`nais.io/v1 Valkey`, `OpenSearch`) that
  already exist in pgrator.
- **naiserator** creates/deletes the two bindings per workload (name
  `<instance>-<app>`), injects the matching env vars, and **stops populating
  `spec.valkey`/`spec.openSearch` on `AivenApplication`**. The legacy `REDIS_*`
  env-var aliases are **dropped** here (revisited separately).
- **aivenator keeps running for Kafka only.** `AivenApplication` is *not*
  retired; it now carries only `spec.kafka`.
- **Janitor coexistence (required Phase-1 task):** aivenator's janitor must stop
  managing Valkey/OpenSearch secrets while continuing to manage Kafka ones, so it
  does not garbage-collect the new pgrator-owned secrets.
- Gaps in scope: secret-key/env-var shaping, password/rotation, secret lifecycle
  (janitor/finalizer → binding-owned), `protected`/`expiresAt`. **Not in scope:**
  PKCS12 credstores and the deterministic-username coupling (both Kafka-only).

### Phase 2 — Kafka

- **`KafkaBinding`** CRD + reconciler, including PKCS12 credstore generation and
  setting `ServiceUser.spec.username` to the deterministic
  `{team}_{app}_{hash}_{suffix}` so kafkarator's wildcard ACLs keep matching.
- naiserator emits `KafkaBinding` instead of `AivenApplication.Spec.Kafka`.
- **Retire aivenator and `AivenApplication`** once Kafka is migrated.

## Consequences

- Phased delivery de-risks the migration: the simpler services move first behind
  binding CRDs, while Kafka's harder concerns are isolated to Phase 2.
- During Phase 1, two systems provision Aiven users simultaneously (pgrator for
  Valkey/OpenSearch, aivenator for Kafka). The janitor-coexistence task is
  essential to avoid cross-deletion of secrets.
- Adopting the official operator removes in-house maintenance of the Aiven API
  integration in aivenator, at the cost of reproducing credstore generation and
  secret-key shaping inside pgrator's binding reconcilers (credstores in Phase 2).
- The architecture becomes uniform across data services: Postgres, Valkey,
  OpenSearch, and Kafka all flow instance-CRD + binding-CRD → aiven-operator,
  reusing pgrator's synchronizer, action system, and golden-test tooling.
- The deterministic Kafka username is a hard coupling with kafkarator; in Phase 2
  the KafkaBinding reconciler must set `ServiceUser.spec.username` explicitly or
  ACL matching silently breaks. This must be coordinated across both operators.
- `protected`/`expiresAt` and the janitor have no direct operator equivalent and
  need an explicit replacement or a conscious drop.
- Dropping `REDIS_*` aliases is a breaking change for apps still reading them;
  it needs its own migration/communication, tracked separately.
- aivenator can be decommissioned only at the end of Phase 2, once Kafka is
  migrated and the last `AivenApplication` consumers are gone.
