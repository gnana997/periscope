package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListPVsArgs struct {
	Cluster clusters.Cluster
}

type GetPVArgs struct {
	Cluster clusters.Cluster
	Name    string
}

func ListPVs(ctx context.Context, p credentials.Provider, args ListPVsArgs) (PVList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PVList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return PVList{}, fmt.Errorf("list pvs: %w", err)
	}
	out := PVList{PVs: make([]PV, 0, len(raw.Items))}
	for _, pv := range raw.Items {
		out.PVs = append(out.PVs, pvSummary(&pv))
	}
	return out, nil
}

func GetPV(ctx context.Context, p credentials.Provider, args GetPVArgs) (PVDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PVDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().PersistentVolumes().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return PVDetail{}, fmt.Errorf("get pv %s: %w", args.Name, err)
	}

	var claimRef *PVClaimRef
	if raw.Spec.ClaimRef != nil {
		claimRef = &PVClaimRef{
			Namespace: raw.Spec.ClaimRef.Namespace,
			Name:      raw.Spec.ClaimRef.Name,
		}
	}

	vm := ""
	if raw.Spec.VolumeMode != nil {
		vm = string(*raw.Spec.VolumeMode)
	}

	return PVDetail{
		PV:          pvSummary(raw),
		ClaimRef:    claimRef,
		VolumeMode:  vm,
		Source:      pvSource(raw),
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetPVYAML(ctx context.Context, p credentials.Provider, args GetPVArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().PersistentVolumes().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pv %s: %w", args.Name, err)
	}
	raw.APIVersion = "v1"
	raw.Kind = "PersistentVolume"
	return formatYAML(raw)
}

func pvSummary(pv *corev1.PersistentVolume) PV {
	sc := ""
	if pv.Spec.StorageClassName != "" {
		sc = pv.Spec.StorageClassName
	}
	rp := ""
	if pv.Spec.PersistentVolumeReclaimPolicy != "" {
		rp = string(pv.Spec.PersistentVolumeReclaimPolicy)
	}
	cap := ""
	if storage, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok {
		cap = storage.String()
	}
	return PV{
		Name:          pv.Name,
		Status:        string(pv.Status.Phase),
		StorageClass:  sc,
		Capacity:      cap,
		AccessModes:   pvcAccessModes(pv.Spec.AccessModes),
		ReclaimPolicy: rp,
		CreatedAt:     pv.CreationTimestamp.Time,
	}
}

// pvSource returns a short human-readable label for the volume's backing store.
func pvSource(pv *corev1.PersistentVolume) string {
	src := pv.Spec.PersistentVolumeSource
	switch {
	case src.CSI != nil:
		return "csi:" + src.CSI.Driver
	case src.NFS != nil:
		return "nfs:" + src.NFS.Server
	case src.HostPath != nil:
		return "hostPath"
	case src.Local != nil:
		return "local"
	case src.AWSElasticBlockStore != nil:
		return "awsEBS"
	case src.GCEPersistentDisk != nil:
		return "gcePD"
	case src.AzureDisk != nil:
		return "azureDisk"
	case src.AzureFile != nil:
		return "azureFile"
	default:
		return ""
	}
}
