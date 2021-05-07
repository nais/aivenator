aivenator:
	go build -o bin/aivenator cmd/aivenator/*.go

test: mocks
	go test ./... -count=1

mocks:
	cd pkg && mockery --all --case snake --inpackage --testonly

integration_test:
	echo "*** Make sure to set the environment AIVEN_TOKEN to a valid token ***"
	go test ./pkg/certificate/... -tags=integration -v -count=1
