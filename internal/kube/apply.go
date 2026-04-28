package kube

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// Apply parses a multi-document YAML manifest and applies each resource
// to the cluster using server-side apply.
func (c *Client) Apply(ctx context.Context, manifest []byte) error {
	objects, err := decodeManifest(manifest)
	if err != nil {
		return fmt.Errorf("decoding manifest: %w", err)
	}

	for _, obj := range objects {
		if err := c.applyObject(ctx, obj); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) applyObject(ctx context.Context, obj *unstructured.Unstructured) error {
	gvk := obj.GroupVersionKind()

	mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("mapping %s %s/%s: %w", gvk.Kind, obj.GetNamespace(), obj.GetName(), err)
	}

	var dr dynamic.ResourceInterface
	switch mapping.Scope.Name() {
	case meta.RESTScopeNameNamespace:
		ns := obj.GetNamespace()
		if ns == "" {
			ns = "default"
		}
		dr = c.dynamic.Resource(mapping.Resource).Namespace(ns)
	default:
		dr = c.dynamic.Resource(mapping.Resource)
	}

	// Remove fields that conflict with server-side apply.
	obj.SetManagedFields(nil)
	unstructured.RemoveNestedField(obj.Object, "metadata", "creationTimestamp")

	data, err := json.Marshal(obj.Object)
	if err != nil {
		return fmt.Errorf("marshaling %s %s: %w", gvk.Kind, obj.GetName(), err)
	}

	_, err = dr.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
		FieldManager: "bink",
	})
	if err != nil {
		return fmt.Errorf("applying %s %s: %w", gvk.Kind, obj.GetName(), err)
	}

	return nil
}

func decodeManifest(manifest []byte) ([]*unstructured.Unstructured, error) {
	var objects []*unstructured.Unstructured
	reader := yamlutil.NewYAMLReader(bufio.NewReader(bytes.NewReader(manifest)))

	for {
		doc, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading YAML document: %w", err)
		}

		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}

		obj := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(doc, &obj.Object); err != nil {
			return nil, fmt.Errorf("unmarshaling YAML document: %w", err)
		}

		if obj.Object == nil {
			continue
		}

		objects = append(objects, obj)
	}

	return objects, nil
}
