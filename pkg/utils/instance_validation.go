package utils

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetResourceInNamespace fetches a CR by name and namespace.
// Returns (obj, nil) when found, (nil, err) otherwise.
// Not-found produces an informative error wrapping ErrNotFound.
func GetResourceInNamespace(ctx context.Context, k8sClient client.Client, obj client.Object, name, namespace string) (client.Object, error) {
	err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj)
	if err == nil {
		return obj, nil
	}
	if k8serrors.IsNotFound(err) {
		return nil, fmt.Errorf("%T %q not found in namespace %q: %w", obj, name, namespace, ErrNotFound)
	}
	return nil, fmt.Errorf("failed to look up %T %q in namespace %q: %w", obj, name, namespace, err)
}
