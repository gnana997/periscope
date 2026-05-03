package k8s

import (
	"context"
	"fmt"

	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// kubernetesIOServiceNameLabel is the label the EndpointSlice
// controller stamps on every slice it owns, pointing at the Service
// that drove its creation. Surfacing it on the list view lets users
// see "which Service does this slice serve" without clicking through
// to the detail panel.
const kubernetesIOServiceNameLabel = "kubernetes.io/service-name"

type ListEndpointSlicesArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

func ListEndpointSlices(ctx context.Context, p credentials.Provider, args ListEndpointSlicesArgs) (EndpointSliceList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return EndpointSliceList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.DiscoveryV1().EndpointSlices(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return EndpointSliceList{}, fmt.Errorf("list endpointslices: %w", err)
	}

	out := EndpointSliceList{EndpointSlices: make([]EndpointSlice, 0, len(raw.Items))}
	for i := range raw.Items {
		out.EndpointSlices = append(out.EndpointSlices, endpointSliceSummary(&raw.Items[i]))
	}
	return out, nil
}

type GetEndpointSliceArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetEndpointSlice(ctx context.Context, p credentials.Provider, args GetEndpointSliceArgs) (EndpointSliceDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return EndpointSliceDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.DiscoveryV1().EndpointSlices(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return EndpointSliceDetail{}, fmt.Errorf("get endpointslice %s/%s: %w", args.Namespace, args.Name, err)
	}
	endpoints := make([]EndpointSliceEndpoint, 0, len(raw.Endpoints))
	for i := range raw.Endpoints {
		endpoints = append(endpoints, endpointSliceEndpointDTO(&raw.Endpoints[i]))
	}
	return EndpointSliceDetail{
		EndpointSlice: endpointSliceSummary(raw),
		Endpoints:     endpoints,
		Labels:        raw.Labels,
		Annotations:   raw.Annotations,
	}, nil
}

func GetEndpointSliceYAML(ctx context.Context, p credentials.Provider, args GetEndpointSliceArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.DiscoveryV1().EndpointSlices(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get endpointslice %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "discovery.k8s.io/v1"
	raw.Kind = "EndpointSlice"
	return formatYAML(raw)
}

// endpointSliceSummary projects a discoveryv1.EndpointSlice to the
// list-view DTO. Same function feeds both ListEndpointSlices and the
// Watch* deltas, so the SPA cache patches against shape-identical
// rows.
func endpointSliceSummary(s *discoveryv1.EndpointSlice) EndpointSlice {
	ports := make([]EndpointSlicePort, 0, len(s.Ports))
	for _, p := range s.Ports {
		ports = append(ports, endpointSlicePortDTO(&p))
	}
	ready, total := 0, 0
	for i := range s.Endpoints {
		total++
		// EndpointConditions.Ready is *bool; nil is interpreted by the
		// Service controller as "not ready" (apiserver convention),
		// which matches what kubectl displays.
		if s.Endpoints[i].Conditions.Ready != nil && *s.Endpoints[i].Conditions.Ready {
			ready++
		}
	}
	return EndpointSlice{
		Name:        s.Name,
		Namespace:   s.Namespace,
		AddressType: string(s.AddressType),
		Ports:       ports,
		ServiceName: s.Labels[kubernetesIOServiceNameLabel],
		ReadyCount:  ready,
		TotalCount:  total,
		CreatedAt:   s.CreationTimestamp.Time,
	}
}

func endpointSlicePortDTO(p *discoveryv1.EndpointPort) EndpointSlicePort {
	dto := EndpointSlicePort{}
	if p.Name != nil {
		dto.Name = *p.Name
	}
	if p.Protocol != nil {
		dto.Protocol = string(*p.Protocol)
	}
	if p.Port != nil {
		dto.Port = *p.Port
	}
	if p.AppProtocol != nil {
		dto.AppProtocol = *p.AppProtocol
	}
	return dto
}

func endpointSliceEndpointDTO(e *discoveryv1.Endpoint) EndpointSliceEndpoint {
	dto := EndpointSliceEndpoint{Addresses: append([]string(nil), e.Addresses...)}
	if e.Hostname != nil {
		dto.Hostname = *e.Hostname
	}
	if e.NodeName != nil {
		dto.NodeName = *e.NodeName
	}
	if e.Zone != nil {
		dto.Zone = *e.Zone
	}
	if e.Conditions.Ready != nil {
		dto.Ready = *e.Conditions.Ready
	}
	if e.Conditions.Serving != nil {
		dto.Serving = *e.Conditions.Serving
	}
	if e.Conditions.Terminating != nil {
		dto.Terminating = *e.Conditions.Terminating
	}
	if e.TargetRef != nil {
		dto.TargetRef = &EndpointSliceTarget{
			Kind:      e.TargetRef.Kind,
			Name:      e.TargetRef.Name,
			Namespace: e.TargetRef.Namespace,
		}
	}
	return dto
}
