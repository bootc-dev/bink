// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestStartCredentialFlagsDefaultToEmpty(t *testing.T) {
	g := NewWithT(t)
	cmd := newStartCmd()

	username, err := cmd.Flags().GetString("registry-user")
	g.Expect(err).ToNot(HaveOccurred())
	password, err := cmd.Flags().GetString("registry-password")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(username).To(BeEmpty())
	g.Expect(password).To(BeEmpty())
}
