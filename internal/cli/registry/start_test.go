// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestStartAuthFlagsRequireCredentials(t *testing.T) {
	g := NewWithT(t)
	cmd := newStartCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--auth"})

	g.Expect(cmd.Execute()).To(MatchError("invalid auth registry credentials: registry username and password are required with --auth"))
}

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
