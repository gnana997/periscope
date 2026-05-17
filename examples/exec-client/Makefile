CONTEXT ?= kind-periscope-demo
POD     ?= busybox-probe
NS      ?= default
CMD     ?= uname -a; ls /
BIN     := bin/exec-client

.PHONY: build
build:
	@mkdir -p bin
	go build -o $(BIN) .

.PHONY: tidy
tidy:
	go mod tidy

# --- cluster lifecycle (only needed for the fallback demo) ---

.PHONY: cluster-ws
cluster-ws:
	kind create cluster --config manifests/kind-ws-on.yaml

.PHONY: cluster-no-ws
cluster-no-ws:
	kind create cluster --config manifests/kind-ws-off.yaml

.PHONY: cluster-ws-down
cluster-ws-down:
	kind delete cluster --name exec-ws

.PHONY: cluster-no-ws-down
cluster-no-ws-down:
	kind delete cluster --name exec-no-ws

# --- exec runs (defaults to whatever CONTEXT you set; busybox-probe ---
# --- needs to exist; the impersonation-client example provisions it) ---

.PHONY: run-auto
run-auto: build
	$(BIN) --context=$(CONTEXT) --namespace=$(NS) --pod=$(POD) \
	  --command="$(CMD)" --protocol=auto

.PHONY: run-ws
run-ws: build
	$(BIN) --context=$(CONTEXT) --namespace=$(NS) --pod=$(POD) \
	  --command="$(CMD)" --protocol=ws

.PHONY: run-spdy
run-spdy: build
	$(BIN) --context=$(CONTEXT) --namespace=$(NS) --pod=$(POD) \
	  --command="$(CMD)" --protocol=spdy

.PHONY: clean
clean:
	rm -rf bin/
