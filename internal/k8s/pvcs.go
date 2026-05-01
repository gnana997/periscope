package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListPVCsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

type GetPVCArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func ListPVCs(ctx context.Context, p credentials.Provider, args ListPVCsArgs) (PVCList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PVCList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().PersistentVolumeClaims(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return PVCList{}, fmt.Errorf("list pvcs: %w", err)
	}
	out := PVCList{PVCs: make([]PVC, 0, len(raw.Items))}
	for _, pvc := range raw.Items {
		out.PVCs = append(out.PVCs, pvcSummary(&pvc))
	}
	return out, nil
}

func GetPVC(ctx context.Context, p credentials.Provider, args GetPVCArgs) (PVCDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PVCDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().PersistentVolumeClaims(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return PVCDetail{}, fmt.Errorf("get pvc %s/%s: %w", args.Namespace, args.Name, err)
	}

	var conditions []PVCCondition
	for _, c := range raw.Status.Conditions {
		conditions = append(conditions, PVCCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	return PVCDetail{
		PVC:         pvcSummary(raw),
		VolumeName:  raw.Spec.VolumeName,
		Conditions:  conditions,
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetPVCYAML(ctx context.Context, p credentials.Provider, args GetPVCArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().PersistentVolumeClaims(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pvc %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "v1"
	raw.Kind = "PersistentVolumeClaim"
	return formatYAML(raw)
}

func pvcSummary(pvc *corev1.PersistentVolumeClaim) PVC {
	sc := ""
	if pvc.Spec.StorageClassName != nil {
		sc = *pvc.Spec.StorageClassName
	}
	return PVC{
		Name:         pvc.Name,
		Namespace:    pvc.Namespace,
		Status:       string(pvc.Status.Phase),
		StorageClass: sc,
		Capacity:     pvcCapacity(pvc),
		AccessModes:  pvcAccessModes(pvc.Spec.AccessModes),
		CreatedAt:    pvc.CreationTimestamp.Time,
	}
}

func pvcCapacity(pvc *corev1.PersistentVolumeClaim) string {
	if cap, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
		return cap.String()
	}
	if req, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		return req.String()
	}
	return ""
}

func pvcAccessModes(modes []corev1.PersistentVolumeAccessMode) []string {
	out := make([]string, len(modes))
	for i, m := range modes {
		out[i] = string(m)
	}
	return out
}
