package mcp_test

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

var _ = Describe("Server", func() {

	var (
		stdin   *bytes.Buffer
		session *gexec.Session
		stop    chan struct{}
	)

	BeforeEach(func() {
		stop = make(chan struct{})

		stdin = &bytes.Buffer{}
		command := exec.Command(exampleServerPath)
		command.Stdin = &blockingReader{r: stdin, done: stop}
		var err error
		session, err = gexec.Start(command, nil, GinkgoWriter)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		stop <- struct{}{}
		session.Wait()
		Eventually(session).Should(gexec.Exit())
	})

	It("responds to an initialization request", func() {
		stdin.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"roots":{"listChanged":true},"sampling":{}},"clientInfo":{"name":"ExampleClient","version":"1.0.0"}}}`)
		Eventually(session.Out.Contents).Should(MatchJSON(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"ExampleServer","version":"1.0.0"}}}`))
	})

	It("responds to pings", func() {
		stdin.WriteString(`{"jsonrpc":"2.0","id":"123","method":"ping"}`)
		Eventually(session.Out.Contents).Should(MatchJSON(`{"jsonrpc":"2.0","id":"123","result":{}}`))
	})

	It("receives notification that the client is initialized", func() {
		stdin.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"roots":{"listChanged":true},"sampling":{}},"clientInfo":{"name":"ExampleClient","version":"1.0.0"}}}`)
		Eventually(session.Out).Should(gbytes.Say("ExampleServer"))

		stdin.WriteString(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

		Consistently(session).ShouldNot(gbytes.Say("error"))
		Consistently(session).ShouldNot(gexec.Exit())
	})

	It("delimits messages with newlines", func() {
		stdin.WriteString(`{"jsonrpc":"2.0","id":"123","method":"ping"}`)
		Eventually(session).Should(gbytes.Say("}\n"))
		stdin.WriteString(`{"jsonrpc":"2.0","id":"234","method":"ping"}`)
		Eventually(session).Should(gbytes.Say("}\n"))
		stdin.WriteString(`{"jsonrpc":"2.0","id":"456","method":"ping"}`)
		Eventually(session).Should(gbytes.Say("}\n"))
	})

	Context("when the method is not recognized", func() {
		It("responds with an error", func() {
			stdin.WriteString(`{"jsonrpc":"2.0","method":"foobar","id":"1"}`)
			Eventually(session.Out.Contents).Should(MatchJSON(`{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":"1"}`))
		})
	})

	Context("when the client protocol version is newer", func() {
		It("responds with the latest version supported by the server", func() {
			stdin.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"3000-01-01","capabilities":{"roots":{"listChanged":true},"sampling":{}},"clientInfo":{"name":"ExampleClient","version":"1.0.0"}}}`)
			Eventually(session.Out.Contents).Should(MatchJSON(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"ExampleServer","version":"1.0.0"}}}`))
		})
	})

	Describe("tools", func() {
		BeforeEach(func() {
			stdin.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"roots":{"listChanged":true},"sampling":{}},"clientInfo":{"name":"ExampleClient","version":"1.0.0"}}}`)
			Eventually(session.Out).Should(gbytes.Say("ExampleServer"))
			stdin.WriteString(`{"jsonrpc":"2.0","method":"initialized"}`)
			Eventually(session.Out).Should(gbytes.Say("\n"))
		})
		Context("when the client requests the list of tools", func() {
			It("responds", func() {
				stdin.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
				Eventually(session.Out).Should(gbytes.Say("tools"))

				Expect(lastResponse(session.Out.Contents())).To(MatchJSON(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"sha256sum","description":"Compute a SHA-256 checksum","inputSchema":{"type":"object","properties":{"text":{"type":"string","description":"Text to compute a checksum for"}},"required":["text"]}}]}}`))
			})
		})
		Context("when the client requests the list of tools with an invalid cursor", func() {
			It("responds with a protocol error", func() {
				stdin.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"cursor":"invalid-cursor"}}`)
				Eventually(session.Out).Should(gbytes.Say("\n"))
				Expect(lastResponse(session.Out.Contents())).To(MatchJSON(`{"jsonrpc":"2.0","error":{"code":-32602,"message":"Invalid params"},"id":2}`))
			})
		})
		Context("when the client calls a tool", func() {
			It("invokes the tool", func() {
				stdin.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sha256sum","arguments":{"text":"the rain in spain falls mainly on the plains"}}}`)
				Eventually(session.Out).Should(gbytes.Say("\n"))
				Expect(lastResponse(session.Out.Contents())).To(MatchJSON(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"b65aacbdd951ff4cd8acef585d482ca4baef81fa0e32132b842fddca3b5590e9"}],"isError":false}}`))
			})
		})
		Context("when the client calls a tool without providing the required arguments", func() {
			It("responds with a protocol error", func() {
				stdin.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sha256sum","arguments":{}}}`)
				Eventually(session.Out).Should(gbytes.Say("\n"))
				Expect(lastResponse(session.Out.Contents())).To(MatchJSON(`{"jsonrpc":"2.0","error":{"code":-32602,"message":"Invalid params"},"id":2}`))
			})
		})
		Context("when the client calls a tool numerous times in a short period", func() {
			It("triggers the rate limit error", func() {
				for i := 0; i < 10; i++ {
					stdin.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sha256sum","arguments":{"text":"the rain in spain falls mainly on the plains"}}}`)
					Eventually(session.Out).Should(gbytes.Say("\n"))
				}
				Expect(lastResponse(session.Out.Contents())).To(MatchJSON(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"rate limit exceeded"}],"isError":true}}`))
			})
		})
		Context("when the client calls a tool that errors", func() {
			It("responds with a tool execution error", func() {
				stdin.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sha256sum","arguments":{"text":""}}}`)
				Eventually(session.Out).Should(gbytes.Say("\n"))
				Expect(lastResponse(session.Out.Contents())).To(MatchJSON(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"failed to compute checksum: text cannot be empty"}],"isError":true}}`))
			})
		})
		Context("when the client calls a tool that does not exist", func() {
			It("responds with an error", func() {
				stdin.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"missing-tool","arguments":{"text":"does not matter"}}}`)
				Eventually(session.Out).Should(gbytes.Say("\n"))
				Expect(lastResponse(session.Out.Contents())).To(MatchJSON(`{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"Unknown tool: missing-tool"}}`))
			})
		})
	})
})

func lastResponse(responses []byte) []byte {
	newline := []byte("\n")
	rs := bytes.Split(bytes.TrimSuffix(responses, newline), newline)
	return rs[len(rs)-1]
}

type blockingReader struct {
	r    io.Reader
	done chan struct{}
}

func (b *blockingReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)

	if err != nil && !errors.Is(err, io.EOF) {
		return n, err
	}

	if errors.Is(err, io.EOF) {
		select {
		case <-b.done:
			return n, err
		case <-time.After(50 * time.Millisecond):
			return n, nil
		}
	}
	return n, nil
}
