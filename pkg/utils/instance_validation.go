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
