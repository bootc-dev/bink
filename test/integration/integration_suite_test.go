package integration_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bootc-dev/bink/internal/config"
	"github.com/bootc-dev/bink/test/integration/helpers"
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bink Integration Suite")
}

var _ = SynchronizedBeforeSuite(func() {
	GinkgoWriter.Println("=== Integration Test Suite Setup ===")

	helpers.RequireCommand("podman")
	helpers.RequireBink()
	helpers.RequireImage(config.DefaultClusterImage)
	helpers.RequireImage(config.DefaultNodeImage)

	GinkgoWriter.Println("✓ All prerequisites verified")
}, func() {})

var _ = SynchronizedAfterSuite(func() {}, func() {
	GinkgoWriter.Println("=== Integration Test Suite Cleanup ===")

	helpers.CleanupAllTestClusters()

	GinkgoWriter.Println("✓ Cleanup complete")
})
