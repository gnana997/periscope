package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListServicesArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

func ListServices(ctx context.Context, p credentials.Provider, args ListServicesArgs) (ServiceList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ServiceList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Services(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ServiceList{}, fmt.Errorf("list services: %w", err)
	}

	out := ServiceList{Services: make([]Service, 0, len(raw.Items))}
	for _, svc := range raw.Items {
		out.Services = append(out.Services, serviceSummary(&svc))
	}
	return out, nil
}

type GetServiceArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetService(ctx context.Context, p credentials.Provider, args GetServiceArgs) (ServiceDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ServiceDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Services(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return ServiceDetail{}, fmt.Errorf("get service %s/%s: %w", args.Namespace, args.Name, err)
	}
	return ServiceDetail{
		Service:         serviceSummary(raw),
		Selector:        raw.Spec.Selector,
		SessionAffinity: string(raw.Spec.SessionAffinity),
		Labels:          raw.Labels,
		Annotations:     raw.Annotations,
	}, nil
}

func GetServiceYAML(ctx context.Context, p credentials.Provider, args GetServiceArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Services(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get service %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "v1"
	raw.Kind = "Service"
	return formatYAML(raw)
}

func serviceSummary(svc *corev1.Service) Service {
	ports := make([]ServicePort, 0, len(svc.Spec.Ports))
	for _, port := range svc.Spec.Ports {
		ports = append(ports, ServicePort{
			Name:       port.Name,
			Protocol:   string(port.Protocol),
			Port:       port.Port,
			TargetPort: port.TargetPort.String(),
			NodePort:   port.NodePort,
		})
	}
	var externalIP string
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		ing := svc.Status.LoadBalancer.Ingress[0]
		if ing.IP != "" {
			externalIP = ing.IP
		} else if ing.Hostname != "" {
			externalIP = ing.Hostname
		}
	}
	return Service{
		Name:       svc.Name,
		Namespace:  svc.Namespace,
		Type:       string(svc.Spec.Type),
		ClusterIP:  svc.Spec.ClusterIP,
		ExternalIP: externalIP,
		Ports:      ports,
		CreatedAt:  svc.CreationTimestamp.Time,
	}
}
