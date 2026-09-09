// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"go.podman.io/podman/v6/libpod/define"
	"go.podman.io/podman/v6/pkg/errorhandling"
	"golang.org/x/crypto/bcrypt"
)

func TestIsContainerAlreadyExists(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "container already exists",
			err:  define.ErrCtrExists,
			want: true,
		},
		{
			name: "wrapped container already exists",
			err:  fmt.Errorf("creating registry container: %w", define.ErrCtrExists),
			want: true,
		},
		{
			name: "name already in use",
			err:  errors.New(`the container name "bink-registry" is already in use by abc123`),
			want: true,
		},
		{
			name: "wrapped name already in use",
			err:  fmt.Errorf("creating container: %w", errors.New(`the container name "bink-registry" is already in use`)),
			want: true,
		},
		{
			name: "conflict",
			err: &errorhandling.ErrorModel{
				ResponseCode: http.StatusConflict,
			},
			want: true,
		},
		{
			name: "not found",
			err: &errorhandling.ErrorModel{
				ResponseCode: http.StatusNotFound,
			},
		},
		{
			name: "unrelated error",
			err:  errors.New("network unreachable"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isContainerAlreadyExists(tt.err)).To(Equal(tt.want))
		})
	}
}

func TestIsPodmanNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "not found",
			err: &errorhandling.ErrorModel{
				ResponseCode: http.StatusNotFound,
			},
			want: true,
		},
		{
			name: "wrapped not found",
			err: fmt.Errorf("removing container: %w", &errorhandling.ErrorModel{
				ResponseCode: http.StatusNotFound,
			}),
			want: true,
		},
		{
			name: "conflict",
			err: &errorhandling.ErrorModel{
				ResponseCode: http.StatusConflict,
			},
		},
		{
			name: "unstructured error",
			err:  errors.New("container not found"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isPodmanNotFound(tt.err)).To(Equal(tt.want))
		})
	}
}

func TestValidateAuthCredentials(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		wantErr  string
	}{
		{name: "valid", username: "test-user", password: "test-password"},
		{name: "empty username", password: "test-password", wantErr: "registry username must not be empty"},
		{name: "empty password", username: "test-user", wantErr: "registry password must not be empty"},
		{name: "colon in username", username: "test:user", password: "test-password", wantErr: "registry username must not contain ':', carriage returns, or newlines"},
		{name: "newline in username", username: "test\nuser", password: "test-password", wantErr: "registry username must not contain ':', carriage returns, or newlines"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			err := ValidateAuthCredentials(tt.username, tt.password)
			if tt.wantErr == "" {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(err).To(MatchError(tt.wantErr))
			}
		})
	}
}

func TestAuthRegistryRequested(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		want     bool
		wantErr  string
	}{
		{name: "no credentials"},
		{name: "both credentials", username: "test-user", password: "test-password", want: true},
		{name: "username only", username: "test-user", wantErr: "registry password must not be empty"},
		{name: "password only", password: "test-password", wantErr: "registry username must not be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			got, err := AuthRegistryRequested(tt.username, tt.password)
			if tt.wantErr == "" {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(err).To(MatchError(tt.wantErr))
			}
			g.Expect(got).To(Equal(tt.want))
		})
	}
}

func TestGenerateHtpasswd(t *testing.T) {
	g := NewWithT(t)
	entry, err := generateHtpasswd("test-user", "test-password")
	g.Expect(err).ToNot(HaveOccurred())

	username, passwordHash, found := strings.Cut(entry, ":")
	g.Expect(found).To(BeTrue())
	g.Expect(username).To(Equal("test-user"))
	g.Expect(bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("test-password"))).To(Succeed())
}
