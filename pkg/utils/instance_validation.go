package utils

import (
	"context"
	"encoding/json"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ReadyState = "RUNNING"
)

// GetResourceInNamespace fetches a CR by name and namespace, and verifies
// that its status.state is "RUNNING".
// Returns (obj, nil) when found and running, (nil, err) otherwise.
func GetResourceInNamespace(ctx context.Context, reader client.Reader, obj client.Object, name, namespace string) (client.Object, error) {
	err := reader.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("%T %q not found in namespace %q: %w", obj, name, namespace, ErrNotFound)
		}
		return nil, fmt.Errorf("failed to look up %T %q in namespace %q: %w", obj, name, namespace, err)
	}

	state, err := extractState(obj)
	if err != nil {
		return nil, fmt.Errorf("%T %q in namespace %q: %w", obj, name, namespace, err)
	}
	if state != ReadyState {
		return nil, fmt.Errorf("%T %q in namespace %q has state %q, expected "+ReadyState+": %w",
			obj, name, namespace, state, ErrNotReady)
	}
	return obj, nil
}

// extractState reads status.state from any object via JSON round-trip of the raw struct.
func extractState(obj client.Object) (string, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("unable to marshal object: %w", err)
	}
	var parsed struct {
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("unable to unmarshal status: %w", err)
	}
	return parsed.Status.State, nil
}
