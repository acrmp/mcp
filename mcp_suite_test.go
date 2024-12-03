package mcp_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

var exampleServerPath string

var _ = BeforeSuite(func() {
	var err error
	exampleServerPath, err = gexec.Build("github.com/acrmp/mcp/example")
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	gexec.CleanupBuildArtifacts()
})

func TestMcp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MCP Suite")
}
