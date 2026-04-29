package kube

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

type Client struct {
	clientset *kubernetes.Clientset
	dynamic   dynamic.Interface
	mapper    *restmapper.DeferredDiscoveryRESTMapper
}

// NewFromKubeconfig creates a Client from raw kubeconfig bytes.
// If serverURL is non-empty, it overrides the server in the kubeconfig
// (e.g. "https://localhost:12345" when connecting through a published port).
func NewFromKubeconfig(ctx context.Context, kubeconfigBytes []byte, serverURL string) (*Client, error) {
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}

	if serverURL != "" {
		config.Host = serverURL
	}

	// The kubeconfig cert is issued for the cluster IP, not localhost,
	// so skip TLS verification when connecting through the published port.
	config.TLSClientConfig.Insecure = true
	config.TLSClientConfig.CAData = nil
	config.TLSClientConfig.CAFile = ""

	if err := waitForAPI(ctx, config); err != nil {
		return nil, fmt.Errorf("waiting for API server: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	discoveryClient := memory.NewMemCacheClient(clientset.Discovery())
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)

	return &Client{
		clientset: clientset,
		dynamic:   dynamicClient,
		mapper:    mapper,
	}, nil
}

func waitForAPI(ctx context.Context, config *rest.Config) error {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	deadline := time.After(2 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timed out waiting for API server at %s", config.Host)
		case <-ticker.C:
			_, err := clientset.Discovery().ServerVersion()
			if err == nil {
				return nil
			}
		}
	}
}
