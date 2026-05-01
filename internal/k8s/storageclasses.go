package k8s

import (
	"context"
	"fmt"

	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListStorageClassesArgs struct {
	Cluster clusters.Cluster
}

type GetStorageClassArgs struct {
	Cluster clusters.Cluster
	Name    string
}

func ListStorageClasses(ctx context.Context, p credentials.Provider, args ListStorageClassesArgs) (StorageClassList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return StorageClassList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return StorageClassList{}, fmt.Errorf("list storageclasses: %w", err)
	}
	out := StorageClassList{StorageClasses: make([]StorageClass, 0, len(raw.Items))}
	for _, sc := range raw.Items {
		out.StorageClasses = append(out.StorageClasses, storageClassSummary(&sc))
	}
	return out, nil
}

func GetStorageClass(ctx context.Context, p credentials.Provider, args GetStorageClassArgs) (StorageClassDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return StorageClassDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.StorageV1().StorageClasses().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return StorageClassDetail{}, fmt.Errorf("get storageclass %s: %w", args.Name, err)
	}
	return StorageClassDetail{
		StorageClass: storageClassSummary(raw),
		Parameters:   raw.Parameters,
		MountOptions: raw.MountOptions,
		Labels:       raw.Labels,
		Annotations:  raw.Annotations,
	}, nil
}

func GetStorageClassYAML(ctx context.Context, p credentials.Provider, args GetStorageClassArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.StorageV1().StorageClasses().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get storageclass %s: %w", args.Name, err)
	}
	raw.APIVersion = "storage.k8s.io/v1"
	raw.Kind = "StorageClass"
	return formatYAML(raw)
}

func storageClassSummary(sc *storagev1.StorageClass) StorageClass {
	rp := ""
	if sc.ReclaimPolicy != nil {
		rp = string(*sc.ReclaimPolicy)
	}
	vbm := ""
	if sc.VolumeBindingMode != nil {
		vbm = string(*sc.VolumeBindingMode)
	}
	allowExpansion := sc.AllowVolumeExpansion != nil && *sc.AllowVolumeExpansion
	return StorageClass{
		Name:                 sc.Name,
		Provisioner:          sc.Provisioner,
		ReclaimPolicy:        rp,
		VolumeBindingMode:    vbm,
		AllowVolumeExpansion: allowExpansion,
		CreatedAt:            sc.CreationTimestamp.Time,
	}
}

