aivenator:
	go build -o bin/aivenator cmd/aivenator/*.go

test:
	go test ./... -count=1

mocks:
	cd pkg && mockery --all --case snake --inpackage --testonly
