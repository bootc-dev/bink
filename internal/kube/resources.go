package kube

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// PatchDeployment applies a JSON patch to a deployment.
func (c *Client) PatchDeployment(ctx context.Context, namespace, name string, patch []byte) error {
	_, err := c.clientset.AppsV1().Deployments(namespace).Patch(
		ctx, name, types.JSONPatchType, patch, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patching deployment %s/%s: %w", namespace, name, err)
	}
	return nil
}

// LabelNode adds the given labels to a node, overwriting any that already exist.
func (c *Client) LabelNode(ctx context.Context, nodeName string, labels map[string]string) error {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": labels,
		},
	}

	data, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling label patch: %w", err)
	}

	_, err = c.clientset.CoreV1().Nodes().Patch(
		ctx, nodeName, types.MergePatchType, data, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("labeling node %s: %w", nodeName, err)
	}
	return nil
}
