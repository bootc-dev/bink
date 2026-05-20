// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package node

import (
	"crypto/md5"
	"fmt"

	"github.com/bootc-dev/bink/internal/config"
)

func CalculateClusterIP(clusterName, nodeName string) string {
	return CalculateClusterIPExcluding(clusterName, nodeName, nil)
}

func CalculateClusterIPExcluding(clusterName, nodeName string, usedIPs []string) string {
	hash := md5.Sum([]byte(clusterName + "/" + nodeName))
	suffix := int(hash[0])%config.ClusterIPRangeSize + config.ClusterIPMinSuffix
	ip := fmt.Sprintf("%s.%d", config.ClusterIPPrefix, suffix)

	for i := 0; i < config.ClusterIPRangeSize; i++ {
		taken := false
		for _, used := range usedIPs {
			if used == ip {
				taken = true
				break
			}
		}
		if !taken {
			return ip
		}
		suffix = (suffix-config.ClusterIPMinSuffix+1)%config.ClusterIPRangeSize + config.ClusterIPMinSuffix
		ip = fmt.Sprintf("%s.%d", config.ClusterIPPrefix, suffix)
	}

	return ip
}

func CalculateClusterMAC(clusterName, nodeName string) string {
	hash := md5.Sum([]byte(clusterName + "/" + nodeName))
	return fmt.Sprintf("%s:%02x:%02x:%02x", config.ClusterMACPrefix, hash[0], hash[1], hash[2])
}
