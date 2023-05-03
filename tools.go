//go:build tools
// +build tools

package tools

import (
	_ "github.com/onsi/ginkgo/v2/ginkgo"
	_ "github.com/vektra/mockery/v2"
	_ "golang.org/x/vuln/cmd/govulncheck"
	_ "honnef.co/go/tools/cmd/staticcheck"
)
