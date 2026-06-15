# Plan: Namespace-scoped instance validation for OpenSearch and Valkey

## Problem

An `AivenApplication` in namespace X can reference any OpenSearch instance by name and get credentials (including `admin` ACL on all indices) for an instance belonging to namespace Y.
The `spec.openSearch.instance` field is used verbatim as the Aiven service name with zero ownership validation.
Valkey already constructs the service name with a namespace prefix (`valkey-<ns>-<instance>`), but still lacks an in-cluster ownership check.

## Solution

Before provisioning credentials, validate that the referenced Aiven service exists in the requesting namespace as an `aiven.io/v1alpha1` CR (`OpenSearch` or `Valkey`).
Additionally, adopt the Valkey-style namespace-prefixed naming for OpenSearch going forward.

## Context: existing architecture

- Aivenator provisions **credentials only** (service users, ACLs, k8s secrets).
  It does NOT create Aiven services.
- The actual services are represented in-cluster as namespaced CRs managed by the Aiven Kubernetes Operator:
  - `opensearches.aiven.io/v1alpha1` (kind: `OpenSearch`)
  - `valkeys.aiven.io/v1alpha1` (kind: `Valkey`)
- These CRs follow naming conventions:
  - OpenSearch: `opensearch-<namespace>-<instance>` (e.g., `opensearch-nais-roger` in namespace `nais`)
  - Valkey: `valkey-<namespace>-<instance>` (e.g., `valkey-nais-foo` in namespace `nais`)
- Kafka is **excluded** from this change (clusters are not per-namespace).

## Current code paths

### OpenSearch (`pkg/services/opensearch/opensearch.go`)

```go
// Line 65: instance used verbatim as Aiven service name
serviceName := spec.Instance

// Line 73: calls Aiven API directly
addresses, err := h.service.GetServiceAddresses(ctx, h.projectName, serviceName)

// Line 75: notFoundIsRecoverable=false → ErrUnrecoverable → no requeue
return nil, utils.AivenFail("GetService", application, err, false, logger)
```

### Valkey (`pkg/services/valkey/valkey.go`)

```go
// Line 73: namespace-prefixed service name
serviceName := fmt.Sprintf("valkey-%s-%s", application.GetNamespace(), valkeySpec.Instance)

// Line 91-93: calls Aiven API; notFoundIsRecoverable=true → ErrNotFound → requeue
addresses, err := h.service.GetServiceAddresses(ctx, h.projectName, serviceName)
if err != nil {
    return nil, utils.AivenFail("GetService", application, err, true, logger)
}
```

### Reconciler error handling (`controllers/aiven_application/reconciler.go`)

```go
// Lines 84-88:
if errors.Is(err, utils.ErrNotFound) {
    cr.RequeueAfter = requeueInterval * 10   // 100s, retries indefinitely
} else if !errors.Is(err, utils.ErrUnrecoverable) {
    cr.RequeueAfter = requeueInterval         // 10s, retries indefinitely
}
// ErrUnrecoverable → no requeue (permanent failure)
```

Failure is always visible via `.status.conditions` (`AivenApplicationAivenFailure`), warning events, and Prometheus metrics regardless of requeue behavior.

## Changes required

### 1. Add `client.Client` to handler structs

Both `OpenSearchHandler` and `ValkeyHandler` need a controller-runtime `client.Client` to perform in-cluster lookups.

**File: `pkg/services/opensearch/opensearch.go`**

```go
type OpenSearchHandler struct {
    serviceuser   serviceuser.ServiceUserManager
    service       service.ServiceManager
    openSearchACL opensearch.ACLManager
    secretConfig  utils.SecretConfig
    projectName   string
    k8sClient     client.Client  // NEW
}
```

**File: `pkg/services/valkey/valkey.go`**

```go
type ValkeyHandler struct {
    serviceuser  serviceuser.ServiceUserManager
    service      service.ServiceManager
    projectName  string
    secretConfig utils.SecretConfig
    k8sClient    client.Client  // NEW
}
```

### 2. Update constructors

**File: `pkg/services/opensearch/opensearch.go`**

```go
func NewOpenSearchHandler(ctx context.Context, aiven *aiven.Client, projectName string, k8sClient client.Client) OpenSearchHandler {
    return OpenSearchHandler{
        // ... existing fields ...
        k8sClient: k8sClient,
    }
}
```

**File: `pkg/services/valkey/valkey.go`**

```go
func NewValkeyHandler(ctx context.Context, aiven *aiven.Client, projectName string, k8sClient client.Client) ValkeyHandler {
    return ValkeyHandler{
        // ... existing fields ...
        k8sClient: k8sClient,
    }
}
```

### 3. Wire `client.Client` through the manager

**File: `pkg/credentials/manager.go`**

Change `NewManager` signature to accept `client.Client` and pass it to handler constructors:

```go
func NewManager(ctx context.Context, aiven *aiven.Client, kafkaProjects []string, mainProjectName string, logger log.FieldLogger, k8sClient client.Client) Manager {
    return Manager{
        handlers: []ServiceHandler{
            kafka.NewKafkaHandler(ctx, aiven, kafkaProjects, mainProjectName, logger),
            opensearch.NewOpenSearchHandler(ctx, aiven, mainProjectName, k8sClient),
            valkey.NewValkeyHandler(ctx, aiven, mainProjectName, k8sClient),
        },
    }
}
```

**File: `cmd/aivenator/main.go`**

In `manageCredentials()`, pass `mgr.GetClient()`:

```go
credentialsManager := credentials.NewManager(ctx, aiven, projects, mainProjectName,
    logger.WithFields(log.Fields{"component": "CredentialsManager"}), mgr.GetClient())
```

### 4. Namespace CR validation in OpenSearch `Apply()`

**File: `pkg/services/opensearch/opensearch.go`**

Replace the current `serviceName := spec.Instance` (line 65) with a resolution function.
Insert this logic **before** the `GetServiceAddresses` call:

```go
func (h OpenSearchHandler) resolveServiceName(ctx context.Context, namespace, instance string) (string, error) {
    // Try new naming convention first: opensearch-<namespace>-<instance>
    newStyleName := fmt.Sprintf("opensearch-%s-%s", namespace, instance)
    if h.crExistsInNamespace(ctx, "opensearches", newStyleName, namespace) {
        return newStyleName, nil
    }

    // Fallback: try bare instance name (legacy)
    if h.crExistsInNamespace(ctx, "opensearches", instance, namespace) {
        return instance, nil
    }

    return "", fmt.Errorf("no OpenSearch CR %q or %q found in namespace %q: %w",
        newStyleName, instance, namespace, utils.ErrNotFound)
}

func (h OpenSearchHandler) crExistsInNamespace(ctx context.Context, resource, name, namespace string) bool {
    obj := &unstructured.Unstructured{}
    obj.SetGroupVersionKind(schema.GroupVersionKind{
        Group:   "aiven.io",
        Version: "v1alpha1",
        Kind:    "OpenSearch",
    })
    err := h.k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj)
    return err == nil
}
```

Then in `Apply()`:

```go
serviceName, err := h.resolveServiceName(ctx, application.GetNamespace(), spec.Instance)
if err != nil {
    utils.LocalFail("ResolveOpenSearchInstance", application, err, logger)
    return nil, err
}
```

### 5. Change OpenSearch `notFoundIsRecoverable` to match Valkey

**File: `pkg/services/opensearch/opensearch.go`**, line 75:

```go
// BEFORE:
return nil, utils.AivenFail("GetService", application, err, false, logger)

// AFTER:
return nil, utils.AivenFail("GetService", application, err, true, logger)
```

This makes OpenSearch retry on Aiven 404 (mirrors Valkey behavior).
The failure is still visible on the resource status/events/metrics.

### 6. Namespace CR validation in Valkey `Apply()`

**File: `pkg/services/valkey/valkey.go`**

After constructing `serviceName` (line 73), insert a check before calling the Aiven API:

```go
serviceName := fmt.Sprintf("valkey-%s-%s", application.GetNamespace(), valkeySpec.Instance)

// NEW: verify the Valkey CR exists in this namespace
if !h.crExistsInNamespace(ctx, serviceName, application.GetNamespace()) {
    err := fmt.Errorf("no Valkey CR %q found in namespace %q: %w",
        serviceName, application.GetNamespace(), utils.ErrNotFound)
    utils.LocalFail("ResolveValkeyInstance", application, err, logger)
    return nil, err
}
```

Helper method:

```go
func (h ValkeyHandler) crExistsInNamespace(ctx context.Context, name, namespace string) bool {
    obj := &unstructured.Unstructured{}
    obj.SetGroupVersionKind(schema.GroupVersionKind{
        Group:   "aiven.io",
        Version: "v1alpha1",
        Kind:    "Valkey",
    })
    err := h.k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj)
    return err == nil
}
```

### 7. RBAC

**File: `charts/aivenator/templates/serviceaccount.yaml`**

Add a new rule block to the `ClusterRole`:

```yaml
  - apiGroups:
      - aiven.io
    resources:
      - opensearches
      - valkeys
    verbs:
      - get
      - list
```

### 8. Typed stubs for `aiven.io/v1alpha1` CRDs

Following the pgrator pattern (`internal/thirdparty/`), write minimal typed stubs instead of using `unstructured.Unstructured`.
This gives compile-time type safety without importing the full `aiven-operator` Go module.
All other nais operators (naiserator, pgrator, kafkarator, bqrator) use typed clients — none use unstructured.

**File: `internal/thirdparty/aiven/types.go`** (new)

```go
package aiven

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Minimal typed stubs for aiven.io/v1alpha1 CRDs.
// Only metadata is needed — we only check existence, not spec/status.

// +kubebuilder:object:root=true
type OpenSearch struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
}

// +kubebuilder:object:root=true
type OpenSearchList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []OpenSearch `json:"items"`
}

// +kubebuilder:object:root=true
type Valkey struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
}

// +kubebuilder:object:root=true
type ValkeyList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []Valkey `json:"items"`
}
```

**File: `internal/thirdparty/aiven/scheme.go`** (new)

```go
package aiven

import (
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/runtime/schema"
)

var SchemeGroupVersion = schema.GroupVersion{Group: "aiven.io", Version: "v1alpha1"}

func AddToScheme(s *runtime.Scheme) error {
    s.AddKnownTypes(SchemeGroupVersion,
        &OpenSearch{}, &OpenSearchList{},
        &Valkey{}, &ValkeyList{},
    )
    return nil
}
```

Register in `cmd/aivenator/main.go` where schemes are loaded:

```go
import thirdparty_aiven "github.com/nais/aivenator/internal/thirdparty/aiven"
// after liberator_scheme.All():
thirdparty_aiven.AddToScheme(scheme)
```

Both handler files then import the typed stubs:

```go
import thirdparty_aiven "github.com/nais/aivenator/internal/thirdparty/aiven"
```

And use typed `client.Get()`:

```go
obj := &thirdparty_aiven.OpenSearch{}
err := h.k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj)
```

### 9. Tests

**File: `pkg/services/opensearch/opensearch_test.go`**

Add test cases:
- **New-style CR exists**: `spec.openSearch.instance = "roger"`, CR `opensearch-<ns>-roger` exists in namespace → success, service name = `opensearch-<ns>-roger`
- **Legacy CR exists**: `spec.openSearch.instance = "old-service"`, no CR `opensearch-<ns>-old-service`, but CR `old-service` exists in namespace → success, service name = `old-service`
- **No CR exists**: `spec.openSearch.instance = "nonexistent"`, neither CR name exists in namespace → error wrapping `ErrNotFound`
- **Cross-namespace attempt**: `spec.openSearch.instance = "roger"`, CR `opensearch-other-ns-roger` exists in namespace `other-ns` but NOT in the requesting namespace → error

Use `fake.NewClientBuilder().WithScheme(scheme).WithObjects(...)` with the typed stubs.

**File: `pkg/services/valkey/valkey_test.go`**

Add test cases:
- **CR exists**: `valkeySpec.Instance = "foo"` in namespace `team-a`, CR `valkey-team-a-foo` exists → success
- **CR missing**: CR `valkey-team-a-foo` does not exist → error wrapping `ErrNotFound`
- **Cross-namespace attempt**: CR exists in `team-b` but not in `team-a` → error

### 10. Shared helper

**File: `pkg/utils/instance_validation.go`** (new)

```go
package utils

import (
    "context"

    "sigs.k8s.io/controller-runtime/pkg/client"
)

// CRExistsInNamespace checks whether a given CR exists in the specified namespace.
func CRExistsInNamespace(ctx context.Context, k8sClient client.Client, obj client.Object, name, namespace string) bool {
    err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj)
    return err == nil
}
```

## Order of operations

1. Add typed stubs (`internal/thirdparty/aiven/`)
2. Register scheme in `cmd/aivenator/main.go`
3. Add shared helper (`pkg/utils/instance_validation.go`)
4. Modify handler structs and constructors (OpenSearch, Valkey)
5. Update `pkg/credentials/manager.go` (`NewManager` signature)
6. Update `cmd/aivenator/main.go` (`manageCredentials` call)
7. Add validation logic inside `Apply()` for both handlers
8. Change OpenSearch `notFoundIsRecoverable` to `true`
9. Add RBAC rules to Helm chart
10. Write/update tests
11. Run `go build ./...` and `go test ./...` to verify

## Behavioral summary

| Scenario | Before | After |
|----------|--------|-------|
| OpenSearch instance referenced from same namespace | Works | Works (resolves via CR lookup) |
| OpenSearch instance referenced from different namespace | Works (security hole) | Fails: CR not found in namespace, requeues with backoff |
| New OpenSearch instance (new naming) | N/A | `spec.openSearch.instance: "roger"` resolves to `opensearch-<ns>-roger` |
| Legacy OpenSearch instance (old naming) | Works | Works via fallback (bare name CR must exist in namespace) |
| Valkey instance from same namespace | Works | Works (CR check added before Aiven API call) |
| Valkey instance name spoofed cross-namespace | Already blocked by service name construction | Now also blocked by CR check |
| OpenSearch Aiven 404 (service provisioning) | Permanent failure, no requeue | Retries every 100s (matches Valkey) |
