package localinstall

import (
	"encoding/xml"
	"testing"
)

func TestWindowsTaskEncodingAndQueryIdentity(t *testing.T) {
	x := `<?xml version="1.0" encoding="UTF-8"?><Task><Principals><Principal><UserId>S-1-fixture</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Actions><Exec><Command>C:\Users\owner\Fased 测试\fased-app.exe</Command><Arguments>serve --data-dir "C:\state space"</Arguments></Exec></Actions></Task>`
	var want taskXML
	if e := xml.Unmarshal([]byte(x), &want); e != nil {
		t.Fatal(e)
	}
	encoded := encodeTaskXML(x)
	if encoded[0] != 255 || encoded[1] != 254 {
		t.Fatal("missing Unicode byte-order mark")
	}
	for _, b := range [][]byte{encoded, encoded[2:], []byte(`<?xml version="1.0" encoding="UTF-16"?>` + x[len(`<?xml version="1.0" encoding="UTF-8"?>`):])} {
		var got taskXML
		if e := xml.Unmarshal(decodeTaskXML(b), &got); e != nil {
			t.Fatal(e)
		}
		if got.Principals != want.Principals || got.Actions.Exec[0] != want.Actions.Exec[0] {
			t.Fatal("Unicode identity changed")
		}
	}
}
