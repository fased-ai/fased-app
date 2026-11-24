package telegram

import "testing"

func FuzzTelegramUpdateParser(f *testing.F) {
	f.Add([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"message_id":2,"chat":{"id":777},"from":{"id":777},"text":"/status@fased_bot"}}]}`), "/approve@bot id123 note", "approve:id123", "123456789:secretTokenValue_1234567890")
	f.Add([]byte(`{"ok":false,"description":"Unauthorized"}`), "/unkill CONFIRM", "kill", "tok")
	f.Add([]byte(`{bad`), "plain text", "invalid:", "")

	f.Fuzz(func(t *testing.T, apiBytes []byte, cmdText, cbData, tok string) {
		_, _ = ParseGetUpdatesResponse(apiBytes)
		_, _ = ParseUpdateJSON(apiBytes)
		_, _ = ParseCommand(cmdText)
		_, _ = ParseCallbackData(cbData)
		redacted := redactToken(string(apiBytes)+" "+tok, tok)
		if tok != "" && len(tok) > 3 && redacted == "" {
			t.Fatal("redacted output empty for non-empty input")
		}
		_, _ = ValidateBotToken(tok)
	})
}
