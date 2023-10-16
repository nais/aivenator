# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.21 as builder

# download kubebuilder and extract it to tmp
ARG BUILDOS BUILDARCH
RUN echo $BUILDOS && \
    echo $BUILDARCH && \
    wget -qO - https://github.com/kubernetes-sigs/kubebuilder/releases/download/v2.3.1/kubebuilder_2.3.1_${BUILDOS}_${BUILDARCH}.tar.gz | tar -xz -C /tmp/

# move to a long-term location and put it on your path
# (you'll need to set the KUBEBUILDER_ASSETS env var if you put it somewhere else)
RUN mv /tmp/kubebuilder_2.3.1_${BUILDOS}_${BUILDARCH} /usr/local/kubebuilder
RUN export PATH=$PATH:/usr/local/kubebuilder/bin

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.* .

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Build dependencies
ARG TARGETOS TARGETARCH
ENV GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN go build std

# Copy rest of project
COPY . /workspace

# Run tests
RUN make test

# Build
RUN CGO_ENABLED=0 go build -a -installsuffix cgo -o aivenator cmd/aivenator/main.go

FROM gcr.io/distroless/static-debian11
WORKDIR /
COPY --from=builder /workspace/aivenator /aivenator

CMD ["/aivenator"]
