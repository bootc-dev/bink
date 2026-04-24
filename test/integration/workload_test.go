package integration_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/bootc-dev/bink/test/integration/helpers"
)

var _ = Describe("Kubernetes Workloads", func() {
	var clusterName string

	BeforeEach(func() {
		clusterName = helpers.GenerateTestClusterName()
	})

	AfterEach(func() {
		helpers.CleanupCluster(clusterName)
	})

	It("should deploy and access workloads on a single-node cluster", func() {
		By("Creating a single-node cluster")
		helpers.CreateCluster(clusterName)

		By("Exposing API and creating Kubernetes client")
		kubeClient, kubeconfigPath := helpers.SetupKubeClient(clusterName)
		defer helpers.CleanupKubeconfig(kubeconfigPath)

		By("Removing control-plane taint to allow scheduling on single-node cluster")
		helpers.RemoveControlPlaneTaint(kubeClient, "node1")

		By("Deploying an nginx pod")
		nginxPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "nginx-test",
				Labels: map[string]string{"run": "nginx-test"},
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{{
					Name:  "nginx",
					Image: "quay.io/fedora/nginx-126:latest",
					Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
				}},
			},
		}
		helpers.CreatePod(kubeClient, "default", nginxPod, 10*time.Minute)

		By("Verifying the pod is running with ready container")
		helpers.WaitForPodReady(kubeClient, "default", "run=nginx-test", 10*time.Minute)

		By("Cleaning up the test pod")
		helpers.DeletePod(kubeClient, "default", "nginx-test")

		By("Creating an nginx Deployment")
		helpers.ApplyDeployment(kubeClient, "default", `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deploy
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx-deploy
  template:
    metadata:
      labels:
        app: nginx-deploy
    spec:
      containers:
      - name: nginx
        image: quay.io/fedora/nginx-126:latest
        ports:
        - containerPort: 8080
`)

		By("Creating a ClusterIP Service")
		helpers.ApplyService(kubeClient, "default", `
apiVersion: v1
kind: Service
metadata:
  name: nginx-deploy
spec:
  selector:
    app: nginx-deploy
  ports:
  - port: 80
    targetPort: 8080
`)

		By("Waiting for the deployment pod to become ready")
		helpers.WaitForPodReady(kubeClient, "default", "app=nginx-deploy", 10*time.Minute)

		By("Getting the ClusterIP service address")
		svc := helpers.SSHExec(clusterName, "node1", "sudo kubectl get svc nginx-deploy -o jsonpath='{.spec.clusterIP}' --kubeconfig=/etc/kubernetes/admin.conf")
		Expect(svc).ToNot(BeEmpty(), "Service should have a ClusterIP")

		By("Verifying the service is reachable from the node")
		httpCode := helpers.SSHExec(clusterName, "node1", fmt.Sprintf("curl -s -o /dev/null -w '%%{http_code}' --max-time 10 http://%s:80", svc))
		Expect(httpCode).To(ContainSubstring("200"), "Service should return HTTP 200")
	})

	It("should schedule pods across multiple nodes", func() {
		By("Creating a cluster with two worker nodes")
		helpers.CreateCluster(clusterName)
		helpers.AddNode(clusterName, "node2", "--role", "worker")
		helpers.AddNode(clusterName, "node3", "--role", "worker")

		By("Exposing API and creating Kubernetes client")
		kubeClient, kubeconfigPath := helpers.SetupKubeClient(clusterName)
		defer helpers.CleanupKubeconfig(kubeconfigPath)

		By("Removing control-plane taint to allow DaemonSet on all nodes")
		helpers.RemoveControlPlaneTaint(kubeClient, "node1")

		By("Waiting for all nodes to be Ready")
		helpers.WaitForNodeReady(kubeClient, "node1", 3*time.Minute)
		helpers.WaitForNodeReady(kubeClient, "node2", 3*time.Minute)
		helpers.WaitForNodeReady(kubeClient, "node3", 3*time.Minute)

		By("Deploying a DaemonSet across all nodes")
		helpers.ApplyDaemonSet(kubeClient, "default", `
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: spread-test
spec:
  selector:
    matchLabels:
      app: spread-test
  template:
    metadata:
      labels:
        app: spread-test
    spec:
      containers:
      - name: busybox
        image: quay.io/libpod/busybox:latest
        command: ["sleep", "3600"]
`)

		By("Waiting for DaemonSet to schedule on all nodes")
		helpers.WaitForDaemonSetReady(kubeClient, "default", "spread-test", 3, 10*time.Minute)

		By("Verifying pods are running on all three nodes")
		podNodes := helpers.GetPodNodes(kubeClient, "default", "app=spread-test")
		Expect(podNodes).To(HaveLen(3))
		Expect(podNodes).To(ContainElement("node1"))
		Expect(podNodes).To(ContainElement("node2"))
		Expect(podNodes).To(ContainElement("node3"))
	})
})
