// impersonation-client is the companion artifact for the Periscope blog post
// on per-user impersonation in Kubernetes. It demonstrates two patterns:
//
//  1. Setting Impersonate-User / Impersonate-Group headers on every
//     client-go request via rest.Config.Impersonate.
//  2. Pre-flighting an action with SelfSubjectAccessReview (SSAR) so the
//     UI can disable a button before the user clicks and gets a 403.
//
// Usage:
//
//	impersonation-client \
//	  --context kind-certwatch \
//	  --as alice@corp --as-group dev \
//	  --verb list --resource pods --namespace default
//
// Add --execute to actually perform the verb if SSAR allowed it. To keep
// the demo safe, --execute only supports get and list; for delete/create/
// patch/update the SSAR verdict is the proof, no mutation is performed.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var resourceGVR = map[string]schema.GroupVersionResource{
	"pods":        {Group: "", Version: "v1", Resource: "pods"},
	"configmaps":  {Group: "", Version: "v1", Resource: "configmaps"},
	"secrets":     {Group: "", Version: "v1", Resource: "secrets"},
	"services":    {Group: "", Version: "v1", Resource: "services"},
	"namespaces":  {Group: "", Version: "v1", Resource: "namespaces"},
	"nodes":       {Group: "", Version: "v1", Resource: "nodes"},
	"deployments": {Group: "apps", Version: "v1", Resource: "deployments"},
}

var mutatingVerbs = map[string]bool{
	"create":           true,
	"update":           true,
	"patch":            true,
	"delete":           true,
	"deletecollection": true,
}

func main() {
	var (
		kubeconfig = flag.String("kubeconfig", defaultKubeconfig(), "path to kubeconfig (default: $KUBECONFIG or ~/.kube/config)")
		ctxName    = flag.String("context", "", "kubeconfig context (default: current-context)")
		asUser     = flag.String("as", "", "impersonate this user (required)")
		asGroups   = flag.String("as-group", "", "impersonate these groups (comma-separated)")
		verb       = flag.String("verb", "list", "verb to check: get, list, watch, create, update, patch, delete, deletecollection")
		resource   = flag.String("resource", "pods", "resource name; one of: pods, deployments, configmaps, secrets, services, namespaces, nodes")
		namespace  = flag.String("namespace", "default", "namespace (empty for cluster-scoped resources)")
		name       = flag.String("name", "", "resource name (optional, used for --execute get)")
		execute    = flag.Bool("execute", false, "if SSAR allows, perform the verb (only get and list are executed; mutations are refused)")
	)
	flag.Parse()
	if *asUser == "" {
		fmt.Fprintln(os.Stderr, "error: --as is required")
		flag.Usage()
		os.Exit(2)
	}
	gvr, known := resourceGVR[*resource]
	if !known {
		fmt.Fprintf(os.Stderr, "error: unknown --resource %q\n", *resource)
		os.Exit(2)
	}

	cfg, err := loadConfig(*kubeconfig, *ctxName)
	if err != nil {
		die("load kubeconfig: %v", err)
	}

	// Set impersonation. From here on, every request this client makes
	// will carry Impersonate-User and Impersonate-Group headers.
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: *asUser,
		Groups:   splitNonEmpty(*asGroups),
	}

	fmt.Println("Impersonation headers that will be sent on every request:")
	fmt.Printf("  Impersonate-User:  %s\n", cfg.Impersonate.UserName)
	for _, g := range cfg.Impersonate.Groups {
		fmt.Printf("  Impersonate-Group: %s\n", g)
	}
	fmt.Println()

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		die("build clientset: %v", err)
	}

	// Pre-flight: SelfSubjectAccessReview. With impersonation set on the
	// rest.Config, the apiserver evaluates the SSAR as the impersonated
	// user — i.e. "can alice@corp do this?" not "can the caller do this?".
	ssar := &authzv1.SelfSubjectAccessReview{
		Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{
				Namespace: *namespace,
				Verb:      *verb,
				Group:     gvr.Group,
				Resource:  gvr.Resource,
				Name:      *name,
			},
		},
	}
	result, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(context.TODO(), ssar, metav1.CreateOptions{})
	if err != nil {
		die("SSAR request failed: %v", err)
	}

	fmt.Printf("SelfSubjectAccessReview verdict for %q on %s/%s in namespace %q:\n",
		*verb, gvrDisplay(gvr), gvr.Resource, *namespace)
	fmt.Printf("  allowed:          %t\n", result.Status.Allowed)
	fmt.Printf("  denied:           %t\n", result.Status.Denied)
	if result.Status.Reason != "" {
		fmt.Printf("  reason:           %s\n", result.Status.Reason)
	}
	if result.Status.EvaluationError != "" {
		fmt.Printf("  evaluation error: %s\n", result.Status.EvaluationError)
	}
	fmt.Println()

	if !*execute {
		fmt.Println("(re-run with --execute to actually perform the verb)")
		return
	}
	if !result.Status.Allowed {
		fmt.Println("Skipping --execute: SSAR did not allow this request.")
		fmt.Println("This is the point of the pre-flight: the UI can disable the button BEFORE")
		fmt.Println("the user clicks and the apiserver returns 403.")
		return
	}
	if mutatingVerbs[*verb] {
		fmt.Printf("Skipping --execute: this demo only executes get and list.\n")
		fmt.Printf("(the SSAR verdict above is the proof — no mutation is performed)\n")
		return
	}

	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		die("build dynamic client: %v", err)
	}

	var ri dynamic.ResourceInterface
	if *namespace != "" {
		ri = dyn.Resource(gvr).Namespace(*namespace)
	} else {
		ri = dyn.Resource(gvr)
	}

	switch *verb {
	case "list":
		list, err := ri.List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			die("list failed: %v", err)
		}
		fmt.Printf("Listed %d %s:\n", len(list.Items), gvr.Resource)
		for _, item := range list.Items {
			ns := item.GetNamespace()
			if ns == "" {
				fmt.Printf("  - %s\n", item.GetName())
				continue
			}
			fmt.Printf("  - %s/%s\n", ns, item.GetName())
		}
	case "get":
		if *name == "" {
			die("--name is required for --execute get")
		}
		obj, err := ri.Get(context.TODO(), *name, metav1.GetOptions{})
		if err != nil {
			die("get failed: %v", err)
		}
		fmt.Printf("Got %s/%s (uid=%s, resourceVersion=%s)\n",
			obj.GetNamespace(), obj.GetName(), obj.GetUID(), obj.GetResourceVersion())
	default:
		fmt.Printf("--execute does not support verb %q (use get or list)\n", *verb)
	}
}

func loadConfig(kubeconfig, ctxName string) (*rest.Config, error) {
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	overrides := &clientcmd.ConfigOverrides{}
	if ctxName != "" {
		overrides.CurrentContext = ctxName
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
}

func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func defaultKubeconfig() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home + "/.kube/config"
	}
	return ""
}

func gvrDisplay(gvr schema.GroupVersionResource) string {
	if gvr.Group == "" {
		return "core/" + gvr.Version
	}
	return gvr.Group + "/" + gvr.Version
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
