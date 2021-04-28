aivenator:
	go build -o bin/aivenator cmd/aivenator/*.go

test:
	go test ./... -count=1
