package mcpclient

import (
	"testing"
)

func FuzzMCPClientJSONRPC(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"quote_lookup"}]}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(`http://127.0.0.1:8080/mcp`))
	f.Add([]byte(`https://169.254.169.254/latest`))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseJSONRPCResult(data)
		if len(data) <= 1024 {
			raw := string(data)
			_, _ = ValidateEndpoint(raw, false)
			_, _ = ValidateEndpoint(raw, true)
			_, _ = NormalizeServerInput(ServerInput{
				ID:       raw,
				Name:     raw,
				Endpoint: raw,
			})
		}
	})
}
