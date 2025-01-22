K8S_VERSION := 1.30.0
arch        := amd64
os          := $(shell uname -s | tr '[:upper:]' '[:lower:]')
testbin_dir := ./.testbin/
tools_archive := kubebuilder-tools-${K8S_VERSION}-$(os)-$(arch).tar.gz

aivenator:
	go build -o bin/aivenator cmd/aivenator/*.go

test:
	go test ./... -v -count=1 -coverprofile cover.out

check:
	go run honnef.co/go/tools/cmd/staticcheck ./...
	go run golang.org/x/vuln/cmd/govulncheck ./...

mocks:
	go run github.com/vektra/mockery/v2@v2.51.1

integration_test: kubebuilder
	echo "*** Make sure to set the environment AIVEN_TOKEN to a valid token ***"
	go test ./pkg/certificate/... ./controllers/... -tags=integration -v -count=1

kubebuilder: $(testbin_dir)$(tools_archive)
	tar -xzf $(testbin_dir)$(tools_archive) --strip-components=2 -C $(testbin_dir)
	chmod -R +x $(testbin_dir)

$(testbin_dir)/$(tools_archive):
	mkdir -p $(testbin_dir)
	curl -L -O --output-dir $(testbin_dir) "https://storage.googleapis.com/kubebuilder-tools/$(tools_archive)"
