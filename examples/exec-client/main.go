// exec-client is the companion artifact for the Periscope blog post
// on pod exec: WebSocket up, SPDY fallback. It demonstrates the
// protocol-negotiation pattern that any production K8s console has
// to implement, because not every cluster's apiserver speaks both.
//
// Modes:
//
//	--protocol=ws    Use NewWebSocketExecutor only. Fails if the
//	                 apiserver refuses the WebSocket upgrade (older
//	                 K8s, RemoteCommandWebsocket feature gate off).
//	--protocol=spdy  Use NewSPDYExecutor only. Always works; the
//	                 ten-year-default for kubectl exec.
//	--protocol=auto  Try WS first; if the apiserver refuses the
//	                 upgrade (httpstream.IsUpgradeFailure), retry
//	                 with SPDY. Both attempts are logged so the
//	                 negotiation is visible in the demo output.
//
// The pod is assumed to already exist — by default `busybox-probe`
// in the `default` namespace, the same pod the impersonation-client
// example uses. To run against a different target use --namespace
// and --pod.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

func main() {
	var (
		kubeconfig = flag.String("kubeconfig", defaultKubeconfig(), "path to kubeconfig (default: $KUBECONFIG or ~/.kube/config)")
		ctxName    = flag.String("context", "", "kubeconfig context (default: current-context)")
		namespace  = flag.String("namespace", "default", "target pod namespace")
		pod        = flag.String("pod", "busybox-probe", "target pod name")
		container  = flag.String("container", "", "target container (default: pod's first container)")
		command    = flag.String("command", "uname -a; ls /", "shell command to exec (wrapped with sh -c)")
		protocol   = flag.String("protocol", "auto", "negotiation: auto | ws | spdy")
	)
	flag.Parse()

	cfg, err := loadConfig(*kubeconfig, *ctxName)
	if err != nil {
		die("load kubeconfig: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		die("build clientset: %v", err)
	}

	// Build the /pods/{name}/exec subresource URL. Both executors
	// receive the same URL; they differ only in HTTP method and the
	// upgrade protocol they negotiate on top.
	execURL := cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(*pod).
		Namespace(*namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: *container,
			Command:   []string{"sh", "-c", *command},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec).URL()

	streamOpts := remotecommand.StreamOptions{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	ctx := context.Background()

	switch *protocol {
	case "spdy":
		fmt.Fprintln(os.Stderr, "[exec-client] protocol=SPDY (forced)")
		exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", execURL)
		if err != nil {
			die("build SPDY executor: %v", err)
		}
		if err := exec.StreamWithContext(ctx, streamOpts); err != nil {
			die("SPDY stream: %v", err)
		}
		fmt.Fprintln(os.Stderr, "[exec-client] protocol=SPDY result=ok")

	case "ws":
		fmt.Fprintln(os.Stderr, "[exec-client] protocol=WebSocket (forced)")
		exec, err := remotecommand.NewWebSocketExecutor(cfg, "GET", execURL.String())
		if err != nil {
			die("build WebSocket executor: %v", err)
		}
		if err := exec.StreamWithContext(ctx, streamOpts); err != nil {
			die("WebSocket stream: %v", err)
		}
		fmt.Fprintln(os.Stderr, "[exec-client] protocol=WebSocket result=ok")

	case "auto":
		fmt.Fprintln(os.Stderr, "[exec-client] auto: attempting WebSocket first...")
		wsExec, err := remotecommand.NewWebSocketExecutor(cfg, "GET", execURL.String())
		if err != nil {
			die("build WebSocket executor: %v", err)
		}
		wsErr := wsExec.StreamWithContext(ctx, streamOpts)
		if wsErr == nil {
			fmt.Fprintln(os.Stderr, "[exec-client] auto: protocol=WebSocket result=ok")
			return
		}
		if !httpstream.IsUpgradeFailure(wsErr) {
			die("WebSocket failed (not an upgrade failure, no fallback): %v", wsErr)
		}
		fmt.Fprintf(os.Stderr, "[exec-client] auto: WS upgrade refused — falling back to SPDY (%v)\n", wsErr)
		spdyExec, err := remotecommand.NewSPDYExecutor(cfg, "POST", execURL)
		if err != nil {
			die("build SPDY executor: %v", err)
		}
		if err := spdyExec.StreamWithContext(ctx, streamOpts); err != nil {
			die("SPDY stream (after WS fallback): %v", err)
		}
		fmt.Fprintln(os.Stderr, "[exec-client] auto: protocol=SPDY result=ok (fallback)")

	default:
		die("unknown --protocol %q (want auto | ws | spdy)", *protocol)
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

func defaultKubeconfig() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home + "/.kube/config"
	}
	return ""
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
