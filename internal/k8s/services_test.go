package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

func TestListServices(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	fakeCS := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web", Namespace: "default", CreationTimestamp: metav1.NewTime(now),
			},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.0.0.10",
				Ports: []corev1.ServicePort{
					{Name: "http", Protocol: corev1.ProtocolTCP, Port: 80, TargetPort: intstr.FromInt(8080)},
					{Name: "https", Protocol: corev1.ProtocolTCP, Port: 443, TargetPort: intstr.FromString("https-target")},
				},
			},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "lb", Namespace: "default", CreationTimestamp: metav1.NewTime(now),
			},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeLoadBalancer,
				ClusterIP: "10.0.0.11",
			},
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{{Hostname: "lb-foo.example.com"}},
				},
			},
		},
	)

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return fakeCS, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	result, err := ListServices(context.Background(), stubProvider{}, ListServicesArgs{
		Cluster: clusters.Cluster{Name: "test", ARN: "arn:aws:eks:us-east-1:1:cluster/test", Region: "us-east-1"},
	})
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if got, want := len(result.Services), 2; got != want {
		t.Fatalf("got %d, want %d", got, want)
	}

	byName := map[string]Service{}
	for _, s := range result.Services {
		byName[s.Name] = s
	}

	web, ok := byName["web"]
	if !ok {
		t.Fatal("missing web service")
	}
	if web.Type != "ClusterIP" {
		t.Errorf("web.Type = %q, want ClusterIP", web.Type)
	}
	if web.ClusterIP != "10.0.0.10" {
		t.Errorf("web.ClusterIP = %q", web.ClusterIP)
	}
	if len(web.Ports) != 2 {
		t.Fatalf("web.Ports len = %d, want 2", len(web.Ports))
	}
	if web.Ports[0].TargetPort != "8080" {
		t.Errorf("web.Ports[0].TargetPort = %q, want 8080 (intstr int)", web.Ports[0].TargetPort)
	}
	if web.Ports[1].TargetPort != "https-target" {
		t.Errorf("web.Ports[1].TargetPort = %q, want https-target (intstr string)", web.Ports[1].TargetPort)
	}

	lb, ok := byName["lb"]
	if !ok {
		t.Fatal("missing lb service")
	}
	if lb.Type != "LoadBalancer" {
		t.Errorf("lb.Type = %q, want LoadBalancer", lb.Type)
	}
	if lb.ExternalIP != "lb-foo.example.com" {
		t.Errorf("lb.ExternalIP = %q, want lb-foo.example.com", lb.ExternalIP)
	}
}
