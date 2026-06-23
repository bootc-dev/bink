// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/bootc-dev/bink/internal/podman"
)

func TestExtractVersionFromTag(t *testing.T) {
	tests := []struct {
		name     string
		imageRef string
		expected string
	}{
		{"standard tag", "ghcr.io/bootc-dev/bink/node:v1.35-fedora-43-disk", "1.35"},
		{"no v prefix", "ghcr.io/bootc-dev/bink/node:1.35-fedora-43", "1.35"},
		{"version only", "registry/repo:v1.30", "1.30"},
		{"latest tag", "registry/repo:latest", ""},
		{"no tag", "registry/repo", ""},
		{"digest reference", "registry/repo:v1.35-fedora@sha256:abc123", "1.35"},
		{"empty string", "", ""},
		{"just name", "myimage", ""},
		{"short tag no dash", "registry/repo:v2", "2"},
		{"complex version", "ghcr.io/org/sub/image:v1.35.2-beta", "1.35.2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractVersionFromTag(tt.imageRef)
			if got != tt.expected {
				t.Errorf("extractVersionFromTag(%q) = %q, want %q", tt.imageRef, got, tt.expected)
			}
		})
	}
}

type mockPodmanClient struct {
	imageLabels      map[string]string
	imageInspectErr  error
	volumeExistsVal  bool
	volumeExistsErr  error
	volumeCreateErr  error
	containerCreated bool
	containerCreateErr error
	containerExecOut string
	containerExecErr error
	containerExecQuietErr error
	containerRunQuietErr  error
	containerExistsVal    bool
	containerWaitCode     int64
	ensureImageErr        error

	createdVolumeName   string
	createdVolumeLabels map[string]string
}

func (m *mockPodmanClient) EnsureImage(_ context.Context, _ string) error {
	return m.ensureImageErr
}

func (m *mockPodmanClient) ImageInspectLabels(_ context.Context, _ string) (map[string]string, error) {
	return m.imageLabels, m.imageInspectErr
}

func (m *mockPodmanClient) VolumeExists(_ context.Context, name string) (bool, error) {
	return m.volumeExistsVal, m.volumeExistsErr
}

func (m *mockPodmanClient) VolumeCreate(_ context.Context, name string, labels map[string]string) error {
	m.createdVolumeName = name
	m.createdVolumeLabels = labels
	return m.volumeCreateErr
}

func (m *mockPodmanClient) ContainerCreate(_ context.Context, _ *podman.ContainerCreateOptions) (string, error) {
	m.containerCreated = true
	return "mock-id", m.containerCreateErr
}

func (m *mockPodmanClient) ContainerExists(_ context.Context, _ string) (bool, error) {
	return m.containerExistsVal, nil
}

func (m *mockPodmanClient) ContainerCopyContent(_ context.Context, _ []byte, _, _ string, _ int64) error {
	return nil
}

func (m *mockPodmanClient) ContainerRemove(_ context.Context, _ string, _ bool) error {
	return nil
}

func (m *mockPodmanClient) ContainerExec(_ context.Context, _ string, _ []string) (string, error) {
	return m.containerExecOut, m.containerExecErr
}

func (m *mockPodmanClient) ContainerExecQuiet(_ context.Context, _ string, _ []string) error {
	return m.containerExecQuietErr
}

func (m *mockPodmanClient) ContainerRunQuiet(_ context.Context, _ string, _ []string, _ []string) error {
	return m.containerRunQuietErr
}

func (m *mockPodmanClient) ContainerWait(_ context.Context, _ string) (int64, error) {
	return m.containerWaitCode, nil
}

func (m *mockPodmanClient) GetPublishedPort(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

func TestGetKubeadmVersionFromImage(t *testing.T) {
	ctx := context.Background()

	t.Run("version from label", func(t *testing.T) {
		mock := &mockPodmanClient{
			imageLabels: map[string]string{"bink.kubeadm-version": "1.35"},
		}
		v, err := GetKubeadmVersionFromImage(ctx, mock, "ghcr.io/bootc-dev/bink/node:v1.35-fedora-43")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v != "1.35" {
			t.Errorf("got %q, want %q", v, "1.35")
		}
	})

	t.Run("image not found falls back to tag", func(t *testing.T) {
		mock := &mockPodmanClient{
			imageInspectErr: fmt.Errorf("image not known"),
		}
		v, err := GetKubeadmVersionFromImage(ctx, mock, "ghcr.io/bootc-dev/bink/node:v1.30-fedora-43")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v != "1.30" {
			t.Errorf("got %q, want %q", v, "1.30")
		}
	})

	t.Run("no such image falls back to tag", func(t *testing.T) {
		mock := &mockPodmanClient{
			imageInspectErr: fmt.Errorf("no such image"),
		}
		v, err := GetKubeadmVersionFromImage(ctx, mock, "registry/node:v2.0-test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v != "2.0" {
			t.Errorf("got %q, want %q", v, "2.0")
		}
	})

	t.Run("label empty falls back to tag", func(t *testing.T) {
		mock := &mockPodmanClient{
			imageLabels: map[string]string{},
		}
		v, err := GetKubeadmVersionFromImage(ctx, mock, "registry/node:v1.28-fedora")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v != "1.28" {
			t.Errorf("got %q, want %q", v, "1.28")
		}
	})

	t.Run("inspect error not image-not-found", func(t *testing.T) {
		mock := &mockPodmanClient{
			imageInspectErr: fmt.Errorf("connection refused"),
		}
		_, err := GetKubeadmVersionFromImage(ctx, mock, "registry/node:v1.35")
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, ErrImageInspect) {
			t.Errorf("got %q, want error wrapping ErrImageInspect", err)
		}
	})

	t.Run("no label and unparseable tag", func(t *testing.T) {
		mock := &mockPodmanClient{
			imageLabels: map[string]string{},
		}
		_, err := GetKubeadmVersionFromImage(ctx, mock, "registry/node:latest")
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, ErrKubeadmVersionUnknown) {
			t.Errorf("got %q, want error wrapping ErrKubeadmVersionUnknown", err)
		}
	})
}

func TestEnsureImagesVolume(t *testing.T) {
	ctx := context.Background()
	nodeImage := "ghcr.io/bootc-dev/bink/node:v1.35-fedora-43"

	t.Run("volume exists and completed", func(t *testing.T) {
		mock := &mockPodmanClient{
			imageLabels:    map[string]string{"bink.kubeadm-version": "1.35"},
			volumeExistsVal: true,
		}
		c := New(Config{Name: "test", PodmanClient: mock})

		vol, err := c.EnsureImagesVolume(ctx, nodeImage)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if vol != "cluster-images-1.35" {
			t.Errorf("got %q, want %q", vol, "cluster-images-1.35")
		}
		if mock.containerCreated {
			t.Error("should not have created populator container")
		}
	})

	t.Run("volume does not exist creates and populates", func(t *testing.T) {
		mock := &mockPodmanClient{
			imageLabels:      map[string]string{"bink.kubeadm-version": "1.35"},
			volumeExistsVal:  false,
			containerExecOut: "registry.k8s.io/kube-apiserver:v1.35.0\nregistry.k8s.io/etcd:3.5.0\n",
		}
		c := New(Config{Name: "test", PodmanClient: mock})

		vol, err := c.EnsureImagesVolume(ctx, nodeImage)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if vol != "cluster-images-1.35" {
			t.Errorf("got %q, want %q", vol, "cluster-images-1.35")
		}
		if mock.createdVolumeName != "cluster-images-1.35" {
			t.Errorf("created volume %q, want %q", mock.createdVolumeName, "cluster-images-1.35")
		}
		if mock.containerCreated != true {
			t.Error("should have created populator container")
		}
	})

	t.Run("ensure image fails", func(t *testing.T) {
		mock := &mockPodmanClient{
			ensureImageErr: fmt.Errorf("pull failed"),
		}
		c := New(Config{Name: "test", PodmanClient: mock})

		_, err := c.EnsureImagesVolume(ctx, nodeImage)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("volume create fails", func(t *testing.T) {
		mock := &mockPodmanClient{
			imageLabels:     map[string]string{"bink.kubeadm-version": "1.35"},
			volumeExistsVal: false,
			volumeCreateErr: fmt.Errorf("disk full"),
		}
		c := New(Config{Name: "test", PodmanClient: mock})

		_, err := c.EnsureImagesVolume(ctx, nodeImage)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("populator container create fails without concurrent population", func(t *testing.T) {
		mock := &mockPodmanClient{
			imageLabels:        map[string]string{"bink.kubeadm-version": "1.35"},
			volumeExistsVal:    false,
			containerCreateErr: fmt.Errorf("name already in use"),
			containerExistsVal: false,
		}
		c := New(Config{Name: "test", PodmanClient: mock})

		_, err := c.EnsureImagesVolume(ctx, nodeImage)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
