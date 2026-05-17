CONTEXT ?= kind-periscope-demo
BIN     := bin/impersonation-client

.PHONY: build
build:
	@mkdir -p bin
	go build -o $(BIN) .

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: apply
apply:
	kubectl --context=$(CONTEXT) apply -f manifests/

.PHONY: unapply
unapply:
	kubectl --context=$(CONTEXT) delete -f manifests/ --ignore-not-found

# Allow case: alice@corp has the pod-reader Role in `default` (from
# manifests/grant-alice.yaml). SSAR should return allowed=true.
.PHONY: run-allow
run-allow: build
	$(BIN) --context=$(CONTEXT) --as alice@corp --as-group dev \
	  --verb list --resource pods --namespace default

# Deny case: nobody granted alice the right to delete pods in kube-system.
# SSAR should return allowed=false; the demo prints the verdict and skips
# the actual call — exactly what a UI does to disable the button.
.PHONY: run-deny
run-deny: build
	$(BIN) --context=$(CONTEXT) --as alice@corp --as-group dev \
	  --verb delete --resource pods --namespace kube-system

# Execute case: same as run-allow but actually lists the pods after
# the SSAR pre-flight clears. This proves the impersonated identity is
# what the apiserver evaluates — the bridge SA never appears in audit.
.PHONY: run-execute
run-execute: build
	$(BIN) --context=$(CONTEXT) --as alice@corp --as-group dev \
	  --verb list --resource pods --namespace default --execute

.PHONY: clean
clean:
	rm -rf bin/
