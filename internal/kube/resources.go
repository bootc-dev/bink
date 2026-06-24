// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
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

// DrainNode cordons the node and evicts all non-DaemonSet pods.
func (c *Client) DrainNode(ctx context.Context, nodeName string) error {
	// Cordon the node
	patch := []byte(`{"spec":{"unschedulable":true}}`)
	_, err := c.clientset.CoreV1().Nodes().Patch(
		ctx, nodeName, types.MergePatchType, patch, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("cordoning node %s: %w", nodeName, err)
	}

	// List pods on this node, excluding DaemonSet-owned and mirror pods
	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
	})
	if err != nil {
		return fmt.Errorf("listing pods on node %s: %w", nodeName, err)
	}

	for _, pod := range podList.Items {
		// Skip DaemonSet-owned pods
		isDaemonSet := false
		for _, ref := range pod.OwnerReferences {
			if ref.Kind == "DaemonSet" {
				isDaemonSet = true
				break
			}
		}
		if isDaemonSet {
			continue
		}

		// Skip mirror pods (static pods managed by kubelet)
		if _, ok := pod.Annotations["kubernetes.io/config.mirror"]; ok {
			continue
		}

		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		}
		err := c.clientset.CoreV1().Pods(pod.Namespace).EvictV1(ctx, eviction)
		if err != nil {
			return fmt.Errorf("evicting pod %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}

	// Wait for pods to be evicted
	deadline := time.After(2 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timed out waiting for pods to drain from node %s", nodeName)
		case <-ticker.C:
			remaining, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
				FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
			})
			if err != nil {
				return fmt.Errorf("listing pods on node %s: %w", nodeName, err)
			}

			nonDaemonSetCount := 0
			for _, pod := range remaining.Items {
				isDaemonSet := false
				for _, ref := range pod.OwnerReferences {
					if ref.Kind == "DaemonSet" {
						isDaemonSet = true
						break
					}
				}
				if _, ok := pod.Annotations["kubernetes.io/config.mirror"]; ok {
					continue
				}
				if !isDaemonSet {
					nonDaemonSetCount++
				}
			}

			if nonDaemonSetCount == 0 {
				return nil
			}
		}
	}
}

// DeleteNode removes the node object from Kubernetes.
func (c *Client) DeleteNode(ctx context.Context, nodeName string) error {
	err := c.clientset.CoreV1().Nodes().Delete(ctx, nodeName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("deleting node %s: %w", nodeName, err)
	}
	return nil
}

// EnableCoreDNSFallthrough patches the CoreDNS ConfigMap so that unresolved
// cluster.local queries fall through to the forward plugin (which reaches the
// bink dnsmasq container via the node's /etc/resolv.conf).
func (c *Client) EnableCoreDNSFallthrough(ctx context.Context) error {
	cm, err := c.clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, "coredns", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting coredns ConfigMap: %w", err)
	}

	corefile, ok := cm.Data["Corefile"]
	if !ok {
		return fmt.Errorf("Corefile key not found in coredns ConfigMap")
	}

	updated := strings.Replace(corefile, "fallthrough in-addr.arpa ip6.arpa", "fallthrough", 1)
	if updated == corefile {
		return nil
	}

	cm.Data["Corefile"] = updated
	_, err = c.clientset.CoreV1().ConfigMaps("kube-system").Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating coredns ConfigMap: %w", err)
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
