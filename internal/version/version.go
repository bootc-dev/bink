// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package version

import "fmt"

var (
	Version   = "dev"
	GitCommit = ""
	BuildDate = ""
)

func Print() string {
	s := fmt.Sprintf("bink version %s", Version)
	if GitCommit != "" {
		s += fmt.Sprintf("\n  commit: %s", GitCommit)
	}
	if BuildDate != "" {
		s += fmt.Sprintf("\n  built:  %s", BuildDate)
	}
	return s
}
