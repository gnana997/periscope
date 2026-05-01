package k8s

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	policyv1 "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// ============================================================
// HorizontalPodAutoscaler
// ============================================================

type ListHPAsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

type GetHPAArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func ListHPAs(ctx context.Context, p credentials.Provider, args ListHPAsArgs) (HPAList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return HPAList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AutoscalingV2().HorizontalPodAutoscalers(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return HPAList{}, fmt.Errorf("list hpas: %w", err)
	}
	out := HPAList{HPAs: make([]HPA, 0, len(raw.Items))}
	for i := range raw.Items {
		out.HPAs = append(out.HPAs, hpaSummary(&raw.Items[i]))
	}
	return out, nil
}

func GetHPA(ctx context.Context, p credentials.Provider, args GetHPAArgs) (HPADetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return HPADetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AutoscalingV2().HorizontalPodAutoscalers(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return HPADetail{}, fmt.Errorf("get hpa %s/%s: %w", args.Namespace, args.Name, err)
	}
	conds := make([]DeploymentCondition, 0, len(raw.Status.Conditions))
	for _, c := range raw.Status.Conditions {
		conds = append(conds, DeploymentCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}
	return HPADetail{
		HPA:         hpaSummary(raw),
		Conditions:  conds,
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetHPAYAML(ctx context.Context, p credentials.Provider, args GetHPAArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AutoscalingV2().HorizontalPodAutoscalers(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get hpa: %w", err)
	}
	return formatYAML(raw)
}

func hpaSummary(h *autoscalingv2.HorizontalPodAutoscaler) HPA {
	minReplicas := int32(1)
	if h.Spec.MinReplicas != nil {
		minReplicas = *h.Spec.MinReplicas
	}
	ready := false
	for _, c := range h.Status.Conditions {
		if c.Type == autoscalingv2.AbleToScale && c.Status == corev1.ConditionTrue {
			ready = true
			break
		}
	}
	return HPA{
		Name:            h.Name,
		Namespace:       h.Namespace,
		CreatedAt:       h.CreationTimestamp.Time,
		Target:          h.Spec.ScaleTargetRef.Kind + "/" + h.Spec.ScaleTargetRef.Name,
		MinReplicas:     minReplicas,
		MaxReplicas:     h.Spec.MaxReplicas,
		CurrentReplicas: h.Status.CurrentReplicas,
		DesiredReplicas: h.Status.DesiredReplicas,
		Ready:           ready,
	}
}

// ============================================================
// PodDisruptionBudget
// ============================================================

type ListPDBsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

type GetPDBArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func ListPDBs(ctx context.Context, p credentials.Provider, args ListPDBsArgs) (PDBList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PDBList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.PolicyV1().PodDisruptionBudgets(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return PDBList{}, fmt.Errorf("list pdbs: %w", err)
	}
	out := PDBList{PDBs: make([]PDB, 0, len(raw.Items))}
	for i := range raw.Items {
		out.PDBs = append(out.PDBs, pdbSummary(&raw.Items[i]))
	}
	return out, nil
}

func GetPDB(ctx context.Context, p credentials.Provider, args GetPDBArgs) (PDBDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PDBDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.PolicyV1().PodDisruptionBudgets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return PDBDetail{}, fmt.Errorf("get pdb %s/%s: %w", args.Namespace, args.Name, err)
	}
	sel := ""
	if raw.Spec.Selector != nil {
		sel = labels.Set(raw.Spec.Selector.MatchLabels).String()
	}
	return PDBDetail{
		PDB:         pdbSummary(raw),
		Selector:    sel,
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetPDBYAML(ctx context.Context, p credentials.Provider, args GetPDBArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.PolicyV1().PodDisruptionBudgets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pdb: %w", err)
	}
	return formatYAML(raw)
}

func pdbSummary(p *policyv1.PodDisruptionBudget) PDB {
	minAvail := "N/A"
	maxUnavail := "N/A"
	if p.Spec.MinAvailable != nil {
		minAvail = intOrStringStr(p.Spec.MinAvailable)
	}
	if p.Spec.MaxUnavailable != nil {
		maxUnavail = intOrStringStr(p.Spec.MaxUnavailable)
	}
	return PDB{
		Name:               p.Name,
		Namespace:          p.Namespace,
		CreatedAt:          p.CreationTimestamp.Time,
		MinAvailable:       minAvail,
		MaxUnavailable:     maxUnavail,
		CurrentHealthy:     p.Status.CurrentHealthy,
		DesiredHealthy:     p.Status.DesiredHealthy,
		ExpectedPods:       p.Status.ExpectedPods,
		DisruptionsAllowed: p.Status.DisruptionsAllowed,
	}
}

func intOrStringStr(v *intstr.IntOrString) string {
	if v == nil {
		return "N/A"
	}
	return v.String()
}

// ============================================================
// ReplicaSet
// ============================================================

type ListReplicaSetsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

type GetReplicaSetArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func ListReplicaSets(ctx context.Context, p credentials.Provider, args ListReplicaSetsArgs) (ReplicaSetList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ReplicaSetList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().ReplicaSets(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ReplicaSetList{}, fmt.Errorf("list replicasets: %w", err)
	}
	out := ReplicaSetList{ReplicaSets: make([]ReplicaSet, 0, len(raw.Items))}
	for i := range raw.Items {
		out.ReplicaSets = append(out.ReplicaSets, replicaSetSummary(&raw.Items[i]))
	}
	return out, nil
}

func GetReplicaSet(ctx context.Context, p credentials.Provider, args GetReplicaSetArgs) (ReplicaSetDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ReplicaSetDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().ReplicaSets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return ReplicaSetDetail{}, fmt.Errorf("get replicaset %s/%s: %w", args.Namespace, args.Name, err)
	}
	conds := make([]DeploymentCondition, 0, len(raw.Status.Conditions))
	for _, c := range raw.Status.Conditions {
		conds = append(conds, DeploymentCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}
	var sel map[string]string
	if raw.Spec.Selector != nil {
		sel = raw.Spec.Selector.MatchLabels
	}
	return ReplicaSetDetail{
		ReplicaSet:  replicaSetSummary(raw),
		Selector:    sel,
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
		Conditions:  conds,
	}, nil
}

func GetReplicaSetYAML(ctx context.Context, p credentials.Provider, args GetReplicaSetArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().ReplicaSets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get replicaset: %w", err)
	}
	return formatYAML(raw)
}

func replicaSetSummary(r *appsv1.ReplicaSet) ReplicaSet {
	owner := ""
	for _, ref := range r.OwnerReferences {
		if ref.Kind == "Deployment" {
			owner = "Deployment/" + ref.Name
			break
		}
	}
	desired := int32(0)
	if r.Spec.Replicas != nil {
		desired = *r.Spec.Replicas
	}
	return ReplicaSet{
		Name:      r.Name,
		Namespace: r.Namespace,
		CreatedAt: r.CreationTimestamp.Time,
		Desired:   desired,
		Current:   r.Status.Replicas,
		Ready:     r.Status.ReadyReplicas,
		Owner:     owner,
	}
}

// ============================================================
// NetworkPolicy
// ============================================================

type ListNetworkPoliciesArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

type GetNetworkPolicyArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func ListNetworkPolicies(ctx context.Context, p credentials.Provider, args ListNetworkPoliciesArgs) (NetworkPolicyList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return NetworkPolicyList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NetworkingV1().NetworkPolicies(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return NetworkPolicyList{}, fmt.Errorf("list networkpolicies: %w", err)
	}
	out := NetworkPolicyList{NetworkPolicies: make([]NetworkPolicy, 0, len(raw.Items))}
	for i := range raw.Items {
		out.NetworkPolicies = append(out.NetworkPolicies, networkPolicySummary(&raw.Items[i]))
	}
	return out, nil
}

func GetNetworkPolicy(ctx context.Context, p credentials.Provider, args GetNetworkPolicyArgs) (NetworkPolicyDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return NetworkPolicyDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NetworkingV1().NetworkPolicies(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return NetworkPolicyDetail{}, fmt.Errorf("get networkpolicy %s/%s: %w", args.Namespace, args.Name, err)
	}
	return NetworkPolicyDetail{
		NetworkPolicy: networkPolicySummary(raw),
		IngressRules:  convertNetworkPolicyRules(raw.Spec.Ingress),
		EgressRules:   convertNetworkPolicyEgressRules(raw.Spec.Egress),
		Labels:        raw.Labels,
		Annotations:   raw.Annotations,
	}, nil
}

func GetNetworkPolicyYAML(ctx context.Context, p credentials.Provider, args GetNetworkPolicyArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NetworkingV1().NetworkPolicies(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get networkpolicy: %w", err)
	}
	return formatYAML(raw)
}

func networkPolicySummary(n *networkingv1.NetworkPolicy) NetworkPolicy {
	policyTypes := make([]string, 0, len(n.Spec.PolicyTypes))
	for _, pt := range n.Spec.PolicyTypes {
		policyTypes = append(policyTypes, string(pt))
	}
	return NetworkPolicy{
		Name:        n.Name,
		Namespace:   n.Namespace,
		CreatedAt:   n.CreationTimestamp.Time,
		PodSelector: labels.Set(n.Spec.PodSelector.MatchLabels).String(),
		PolicyTypes: policyTypes,
	}
}

func convertNetworkPolicyRules(rules []networkingv1.NetworkPolicyIngressRule) []NetworkPolicyRule {
	out := make([]NetworkPolicyRule, 0, len(rules))
	for _, r := range rules {
		ports := npPorts(r.Ports)
		peers := make([]string, 0, len(r.From))
		for _, peer := range r.From {
			peers = append(peers, networkPolicyPeerStr(peer.NamespaceSelector, peer.PodSelector, peer.IPBlock))
		}
		out = append(out, NetworkPolicyRule{Ports: ports, Peers: peers})
	}
	return out
}

func convertNetworkPolicyEgressRules(rules []networkingv1.NetworkPolicyEgressRule) []NetworkPolicyRule {
	out := make([]NetworkPolicyRule, 0, len(rules))
	for _, r := range rules {
		ports := npPorts(r.Ports)
		peers := make([]string, 0, len(r.To))
		for _, peer := range r.To {
			peers = append(peers, networkPolicyPeerStr(peer.NamespaceSelector, peer.PodSelector, peer.IPBlock))
		}
		out = append(out, NetworkPolicyRule{Ports: ports, Peers: peers})
	}
	return out
}

func npPorts(ports []networkingv1.NetworkPolicyPort) []string {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		proto := "TCP"
		if p.Protocol != nil {
			proto = string(*p.Protocol)
		}
		portStr := ""
		if p.Port != nil {
			portStr = p.Port.String()
		}
		out = append(out, proto+":"+portStr)
	}
	return out
}

func networkPolicyPeerStr(nsSel, podSel *metav1.LabelSelector, ipBlock *networkingv1.IPBlock) string {
	if ipBlock != nil {
		return ipBlock.CIDR
	}
	parts := []string{}
	if nsSel != nil {
		parts = append(parts, "ns:"+labels.Set(nsSel.MatchLabels).String())
	}
	if podSel != nil {
		parts = append(parts, "pod:"+labels.Set(podSel.MatchLabels).String())
	}
	if len(parts) == 0 {
		return "(all)"
	}
	return strings.Join(parts, "/")
}

// ============================================================
// IngressClass
// ============================================================

type ListIngressClassesArgs struct {
	Cluster clusters.Cluster
}

type GetIngressClassArgs struct {
	Cluster clusters.Cluster
	Name    string
}

func ListIngressClasses(ctx context.Context, p credentials.Provider, args ListIngressClassesArgs) (IngressClassList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return IngressClassList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NetworkingV1().IngressClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return IngressClassList{}, fmt.Errorf("list ingressclasses: %w", err)
	}
	out := IngressClassList{IngressClasses: make([]IngressClass, 0, len(raw.Items))}
	for i := range raw.Items {
		out.IngressClasses = append(out.IngressClasses, ingressClassSummary(&raw.Items[i]))
	}
	return out, nil
}

func GetIngressClass(ctx context.Context, p credentials.Provider, args GetIngressClassArgs) (IngressClassDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return IngressClassDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NetworkingV1().IngressClasses().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return IngressClassDetail{}, fmt.Errorf("get ingressclass %s: %w", args.Name, err)
	}
	params := ""
	if raw.Spec.Parameters != nil {
		params = raw.Spec.Parameters.Kind + "/" + raw.Spec.Parameters.Name
		if raw.Spec.Parameters.APIGroup != nil && *raw.Spec.Parameters.APIGroup != "" {
			params = *raw.Spec.Parameters.APIGroup + "/" + params
		}
	}
	return IngressClassDetail{
		IngressClass: ingressClassSummary(raw),
		Parameters:   params,
		Labels:       raw.Labels,
		Annotations:  raw.Annotations,
	}, nil
}

func GetIngressClassYAML(ctx context.Context, p credentials.Provider, args GetIngressClassArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NetworkingV1().IngressClasses().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get ingressclass: %w", err)
	}
	return formatYAML(raw)
}

func ingressClassSummary(ic *networkingv1.IngressClass) IngressClass {
	isDefault := ic.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true"
	return IngressClass{
		Name:       ic.Name,
		CreatedAt:  ic.CreationTimestamp.Time,
		Controller: ic.Spec.Controller,
		IsDefault:  isDefault,
	}
}

// ============================================================
// ResourceQuota
// ============================================================

type ListResourceQuotasArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

type GetResourceQuotaArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func ListResourceQuotas(ctx context.Context, p credentials.Provider, args ListResourceQuotasArgs) (ResourceQuotaList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ResourceQuotaList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().ResourceQuotas(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ResourceQuotaList{}, fmt.Errorf("list resourcequotas: %w", err)
	}
	out := ResourceQuotaList{ResourceQuotas: make([]ResourceQuota, 0, len(raw.Items))}
	for i := range raw.Items {
		out.ResourceQuotas = append(out.ResourceQuotas, resourceQuotaSummary(&raw.Items[i]))
	}
	return out, nil
}

func GetResourceQuota(ctx context.Context, p credentials.Provider, args GetResourceQuotaArgs) (ResourceQuota, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ResourceQuota{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().ResourceQuotas(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return ResourceQuota{}, fmt.Errorf("get resourcequota %s/%s: %w", args.Namespace, args.Name, err)
	}
	return resourceQuotaSummary(raw), nil
}

func GetResourceQuotaYAML(ctx context.Context, p credentials.Provider, args GetResourceQuotaArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().ResourceQuotas(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get resourcequota: %w", err)
	}
	return formatYAML(raw)
}

func resourceQuotaSummary(q *corev1.ResourceQuota) ResourceQuota {
	items := make(map[string]QuotaEntry, len(q.Status.Hard))
	for resourceName, hardQty := range q.Status.Hard {
		used := ""
		if usedQty, ok := q.Status.Used[resourceName]; ok {
			used = usedQty.String()
		}
		items[string(resourceName)] = QuotaEntry{
			Hard: hardQty.String(),
			Used: used,
		}
	}
	return ResourceQuota{
		Name:      q.Name,
		Namespace: q.Namespace,
		CreatedAt: q.CreationTimestamp.Time,
		Items:     items,
	}
}

// ============================================================
// LimitRange
// ============================================================

type ListLimitRangesArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

type GetLimitRangeArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func ListLimitRanges(ctx context.Context, p credentials.Provider, args ListLimitRangesArgs) (LimitRangeList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return LimitRangeList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().LimitRanges(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return LimitRangeList{}, fmt.Errorf("list limitranges: %w", err)
	}
	out := LimitRangeList{LimitRanges: make([]LimitRange, 0, len(raw.Items))}
	for i := range raw.Items {
		lr := &raw.Items[i]
		out.LimitRanges = append(out.LimitRanges, LimitRange{
			Name:       lr.Name,
			Namespace:  lr.Namespace,
			CreatedAt:  lr.CreationTimestamp.Time,
			LimitCount: len(lr.Spec.Limits),
		})
	}
	return out, nil
}

func GetLimitRange(ctx context.Context, p credentials.Provider, args GetLimitRangeArgs) (LimitRangeDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return LimitRangeDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().LimitRanges(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return LimitRangeDetail{}, fmt.Errorf("get limitrange %s/%s: %w", args.Namespace, args.Name, err)
	}
	limits := make([]LimitRangeItem, 0, len(raw.Spec.Limits))
	for _, limit := range raw.Spec.Limits {
		limits = append(limits, LimitRangeItem{
			Type:                 string(limit.Type),
			Default:              quantityMapToStr(limit.Default),
			DefaultRequest:       quantityMapToStr(limit.DefaultRequest),
			Max:                  quantityMapToStr(limit.Max),
			Min:                  quantityMapToStr(limit.Min),
			MaxLimitRequestRatio: quantityMapToStr(limit.MaxLimitRequestRatio),
		})
	}
	return LimitRangeDetail{
		LimitRange: LimitRange{
			Name:       raw.Name,
			Namespace:  raw.Namespace,
			CreatedAt:  raw.CreationTimestamp.Time,
			LimitCount: len(raw.Spec.Limits),
		},
		Limits:      limits,
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetLimitRangeYAML(ctx context.Context, p credentials.Provider, args GetLimitRangeArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().LimitRanges(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get limitrange: %w", err)
	}
	return formatYAML(raw)
}

func quantityMapToStr(m corev1.ResourceList) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[string(k)] = v.String()
	}
	return out
}

// ============================================================
// PriorityClass
// ============================================================

type ListPriorityClassesArgs struct {
	Cluster clusters.Cluster
}

type GetPriorityClassArgs struct {
	Cluster clusters.Cluster
	Name    string
}

func ListPriorityClasses(ctx context.Context, p credentials.Provider, args ListPriorityClassesArgs) (PriorityClassList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PriorityClassList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.SchedulingV1().PriorityClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return PriorityClassList{}, fmt.Errorf("list priorityclasses: %w", err)
	}
	out := PriorityClassList{PriorityClasses: make([]PriorityClass, 0, len(raw.Items))}
	for i := range raw.Items {
		out.PriorityClasses = append(out.PriorityClasses, priorityClassSummary(&raw.Items[i]))
	}
	return out, nil
}

func GetPriorityClass(ctx context.Context, p credentials.Provider, args GetPriorityClassArgs) (PriorityClassDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PriorityClassDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.SchedulingV1().PriorityClasses().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return PriorityClassDetail{}, fmt.Errorf("get priorityclass %s: %w", args.Name, err)
	}
	return PriorityClassDetail{
		PriorityClass: priorityClassSummary(raw),
		Description:   raw.Description,
		Labels:        raw.Labels,
		Annotations:   raw.Annotations,
	}, nil
}

func GetPriorityClassYAML(ctx context.Context, p credentials.Provider, args GetPriorityClassArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.SchedulingV1().PriorityClasses().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get priorityclass: %w", err)
	}
	return formatYAML(raw)
}

func priorityClassSummary(pc *schedulingv1.PriorityClass) PriorityClass {
	preemption := "PreemptLowerPriority"
	if pc.PreemptionPolicy != nil {
		preemption = string(*pc.PreemptionPolicy)
	}
	return PriorityClass{
		Name:             pc.Name,
		CreatedAt:        pc.CreationTimestamp.Time,
		Value:            pc.Value,
		GlobalDefault:    pc.GlobalDefault,
		PreemptionPolicy: preemption,
	}
}

// ============================================================
// RuntimeClass
// ============================================================

type ListRuntimeClassesArgs struct {
	Cluster clusters.Cluster
}

type GetRuntimeClassArgs struct {
	Cluster clusters.Cluster
	Name    string
}

func ListRuntimeClasses(ctx context.Context, p credentials.Provider, args ListRuntimeClassesArgs) (RuntimeClassList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return RuntimeClassList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NodeV1().RuntimeClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return RuntimeClassList{}, fmt.Errorf("list runtimeclasses: %w", err)
	}
	out := RuntimeClassList{RuntimeClasses: make([]RuntimeClass, 0, len(raw.Items))}
	for i := range raw.Items {
		out.RuntimeClasses = append(out.RuntimeClasses, runtimeClassSummary(&raw.Items[i]))
	}
	return out, nil
}

func GetRuntimeClass(ctx context.Context, p credentials.Provider, args GetRuntimeClassArgs) (RuntimeClassDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return RuntimeClassDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NodeV1().RuntimeClasses().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return RuntimeClassDetail{}, fmt.Errorf("get runtimeclass %s: %w", args.Name, err)
	}
	var nodeSelector map[string]string
	var tolerations []string
	if raw.Scheduling != nil {
		nodeSelector = raw.Scheduling.NodeSelector
		for _, t := range raw.Scheduling.Tolerations {
			tolerations = append(tolerations, fmt.Sprintf("%s=%s:%s", t.Key, t.Value, string(t.Effect)))
		}
	}
	return RuntimeClassDetail{
		RuntimeClass: runtimeClassSummary(raw),
		NodeSelector: nodeSelector,
		Tolerations:  tolerations,
		Labels:       raw.Labels,
		Annotations:  raw.Annotations,
	}, nil
}

func GetRuntimeClassYAML(ctx context.Context, p credentials.Provider, args GetRuntimeClassArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.NodeV1().RuntimeClasses().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get runtimeclass: %w", err)
	}
	return formatYAML(raw)
}

func runtimeClassSummary(rc *nodev1.RuntimeClass) RuntimeClass {
	cpuOverhead := ""
	memOverhead := ""
	if rc.Overhead != nil {
		if v, ok := rc.Overhead.PodFixed[corev1.ResourceCPU]; ok {
			cpuOverhead = v.String()
		}
		if v, ok := rc.Overhead.PodFixed[corev1.ResourceMemory]; ok {
			memOverhead = v.String()
		}
	}
	return RuntimeClass{
		Name:           rc.Name,
		CreatedAt:      rc.CreationTimestamp.Time,
		Handler:        rc.Handler,
		CPUOverhead:    cpuOverhead,
		MemoryOverhead: memOverhead,
	}
}
