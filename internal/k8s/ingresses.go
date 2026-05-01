package k8s

import (
	"context"
	"fmt"
	"strconv"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListIngressesArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

func ListIngresses(ctx context.Context, p credentials.Provider, args ListIngressesArgs) (IngressList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return IngressList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NetworkingV1().Ingresses(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return IngressList{}, fmt.Errorf("list ingresses: %w", err)
	}

	out := IngressList{Ingresses: make([]Ingress, 0, len(raw.Items))}
	for _, ing := range raw.Items {
		out.Ingresses = append(out.Ingresses, ingressSummary(&ing))
	}
	return out, nil
}

type GetIngressArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetIngress(ctx context.Context, p credentials.Provider, args GetIngressArgs) (IngressDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return IngressDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NetworkingV1().Ingresses(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return IngressDetail{}, fmt.Errorf("get ingress %s/%s: %w", args.Namespace, args.Name, err)
	}

	rules := make([]IngressRule, 0, len(raw.Spec.Rules))
	for _, r := range raw.Spec.Rules {
		rules = append(rules, convertRule(r))
	}

	tls := make([]IngressTLS, 0, len(raw.Spec.TLS))
	for _, t := range raw.Spec.TLS {
		tls = append(tls, IngressTLS{
			Hosts:      append([]string(nil), t.Hosts...),
			SecretName: t.SecretName,
		})
	}

	return IngressDetail{
		Ingress:     ingressSummary(raw),
		Rules:       rules,
		TLS:         tls,
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetIngressYAML(ctx context.Context, p credentials.Provider, args GetIngressArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NetworkingV1().Ingresses(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get ingress %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "networking.k8s.io/v1"
	raw.Kind = "Ingress"
	return formatYAML(raw)
}

func ingressSummary(ing *networkingv1.Ingress) Ingress {
	hostSet := map[string]struct{}{}
	hosts := []string{}
	for _, r := range ing.Spec.Rules {
		if r.Host == "" {
			continue
		}
		if _, dup := hostSet[r.Host]; !dup {
			hostSet[r.Host] = struct{}{}
			hosts = append(hosts, r.Host)
		}
	}
	for _, t := range ing.Spec.TLS {
		for _, h := range t.Hosts {
			if _, dup := hostSet[h]; !dup {
				hostSet[h] = struct{}{}
				hosts = append(hosts, h)
			}
		}
	}

	var class string
	if ing.Spec.IngressClassName != nil {
		class = *ing.Spec.IngressClassName
	}

	var address string
	if len(ing.Status.LoadBalancer.Ingress) > 0 {
		lb := ing.Status.LoadBalancer.Ingress[0]
		if lb.Hostname != "" {
			address = lb.Hostname
		} else if lb.IP != "" {
			address = lb.IP
		}
	}

	return Ingress{
		Name:      ing.Name,
		Namespace: ing.Namespace,
		Class:     class,
		Hosts:     hosts,
		Address:   address,
		CreatedAt: ing.CreationTimestamp.Time,
	}
}

func convertRule(r networkingv1.IngressRule) IngressRule {
	out := IngressRule{Host: r.Host}
	if r.HTTP == nil {
		return out
	}
	out.Paths = make([]IngressPath, 0, len(r.HTTP.Paths))
	for _, p := range r.HTTP.Paths {
		var pt string
		if p.PathType != nil {
			pt = string(*p.PathType)
		}
		out.Paths = append(out.Paths, IngressPath{
			Path:     p.Path,
			PathType: pt,
			Backend:  convertBackend(p.Backend),
		})
	}
	return out
}

func convertBackend(b networkingv1.IngressBackend) IngressBackend {
	if b.Service == nil {
		// Resource-backed (CRD); rare, skip in v1.
		return IngressBackend{}
	}
	port := ""
	if b.Service.Port.Name != "" {
		port = b.Service.Port.Name
	} else if b.Service.Port.Number != 0 {
		port = strconv.Itoa(int(b.Service.Port.Number))
	}
	return IngressBackend{
		ServiceName: b.Service.Name,
		ServicePort: port,
	}
}
