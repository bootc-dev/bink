package helpers

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"strings"
)

// NewKubeClient creates a Kubernetes clientset from a kubeconfig file.
func NewKubeClient(kubeconfigPath string) *kubernetes.Clientset {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	Expect(err).ToNot(HaveOccurred(), "Failed to build kubeconfig from %s", kubeconfigPath)

	config.TLSClientConfig.Insecure = true
	config.TLSClientConfig.CAData = nil
	config.TLSClientConfig.CAFile = ""

	clientset, err := kubernetes.NewForConfig(config)
	Expect(err).ToNot(HaveOccurred(), "Failed to create Kubernetes client")
	return clientset
}

// SetupKubeClient exposes the API for a cluster and returns a clientset and the kubeconfig path.
func SetupKubeClient(clusterName string) (*kubernetes.Clientset, string) {
	kubeconfigPath := fmt.Sprintf("../../kubeconfig-%s", clusterName)
	ExposeAPI(clusterName, kubeconfigPath)
	client := NewKubeClient(kubeconfigPath)
	return client, kubeconfigPath
}

// CleanupKubeconfig removes the kubeconfig file.
func CleanupKubeconfig(kubeconfigPath string) {
	os.Remove(kubeconfigPath)
}

// WaitForNodeReady polls until the target node shows Ready status.
func WaitForNodeReady(client *kubernetes.Clientset, targetNodeName string, timeout time.Duration) {
	GinkgoWriter.Printf("Waiting for node %s to become Ready (timeout: %s)\n", targetNodeName, timeout)
	Eventually(func() bool {
		node, err := client.CoreV1().Nodes().Get(context.Background(), targetNodeName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				return true
			}
		}
		return false
	}, timeout, 10*time.Second).Should(BeTrue(), "Node %s should become Ready", targetNodeName)
}

// WaitForPodReady polls until a pod matching the label selector is Running with all containers ready.
func WaitForPodReady(client *kubernetes.Clientset, namespace, labelSelector string, timeout time.Duration) {
	GinkgoWriter.Printf("Waiting for pod with label %s in namespace %s to be Running (timeout: %s)\n", labelSelector, namespace, timeout)
	Eventually(func() bool {
		pods, err := client.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil || len(pods.Items) == 0 {
			return false
		}
		pod := pods.Items[0]
		if pod.Status.Phase != corev1.PodRunning {
			return false
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if !cs.Ready {
				return false
			}
		}
		return true
	}, timeout, 10*time.Second).Should(BeTrue(), "Pod with label %s should be Running and ready", labelSelector)
}

// GetNodeCount returns the number of Kubernetes nodes in the cluster.
func GetNodeCount(client *kubernetes.Clientset) int {
	nodes, err := client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred(), "Failed to list nodes")
	return len(nodes.Items)
}

// CreatePod creates a pod from the given spec and waits for it to be Running.
func CreatePod(client *kubernetes.Clientset, namespace string, pod *corev1.Pod, timeout time.Duration) {
	_, err := client.CoreV1().Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred(), "Failed to create pod %s", pod.Name)
	WaitForPodReady(client, namespace, fmt.Sprintf("run=%s", pod.Name), timeout)
}

// DeletePod deletes a pod. Does not fail if already gone.
func DeletePod(client *kubernetes.Clientset, namespace, name string) {
	_ = client.CoreV1().Pods(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
}

// ApplyDeployment parses a YAML string into a Deployment and creates it.
func ApplyDeployment(client *kubernetes.Clientset, namespace, manifest string) *appsv1.Deployment {
	deployment := &appsv1.Deployment{}
	err := yaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096).Decode(deployment)
	Expect(err).ToNot(HaveOccurred(), "Failed to parse deployment manifest")

	created, err := client.AppsV1().Deployments(namespace).Create(context.Background(), deployment, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred(), "Failed to create deployment %s", deployment.Name)
	return created
}

// ApplyService parses a YAML string into a Service and creates it.
func ApplyService(client *kubernetes.Clientset, namespace, manifest string) *corev1.Service {
	service := &corev1.Service{}
	err := yaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096).Decode(service)
	Expect(err).ToNot(HaveOccurred(), "Failed to parse service manifest")

	created, err := client.CoreV1().Services(namespace).Create(context.Background(), service, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred(), "Failed to create service %s", service.Name)
	return created
}

// ApplyDaemonSet parses a YAML string into a DaemonSet and creates it.
func ApplyDaemonSet(client *kubernetes.Clientset, namespace, manifest string) *appsv1.DaemonSet {
	daemonSet := &appsv1.DaemonSet{}
	err := yaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096).Decode(daemonSet)
	Expect(err).ToNot(HaveOccurred(), "Failed to parse daemonset manifest")

	created, err := client.AppsV1().DaemonSets(namespace).Create(context.Background(), daemonSet, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred(), "Failed to create daemonset %s", daemonSet.Name)
	return created
}

// WaitForDaemonSetReady polls until the DaemonSet has the expected number of ready pods.
func WaitForDaemonSetReady(client *kubernetes.Clientset, namespace, name string, expectedReady int32, timeout time.Duration) {
	GinkgoWriter.Printf("Waiting for DaemonSet %s to have %d ready pods (timeout: %s)\n", name, expectedReady, timeout)
	Eventually(func() int32 {
		ds, err := client.AppsV1().DaemonSets(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return 0
		}
		return ds.Status.NumberReady
	}, timeout, 10*time.Second).Should(Equal(expectedReady), "DaemonSet %s should have %d ready pods", name, expectedReady)
}

// GetPodNodes returns a list of node names where pods with the given label are running.
func GetPodNodes(client *kubernetes.Clientset, namespace, labelSelector string) []string {
	pods, err := client.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	Expect(err).ToNot(HaveOccurred(), "Failed to list pods")

	var nodes []string
	for _, pod := range pods.Items {
		nodes = append(nodes, pod.Spec.NodeName)
	}
	return nodes
}
