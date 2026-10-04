package platform

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDjangoSigningInteroperability(t *testing.T) {
	raw, err := os.ReadFile("testdata/signing-python.json")
	if err != nil {
		t.Fatal(err)
	}
	type payload struct {
		Seed   string `json:"seed"`
		Offset int    `json:"offset"`
	}
	var cases []struct {
		Payload           payload
		Token, Compressed string
	}
	if json.Unmarshal(raw, &cases) != nil {
		t.Fatal("invalid oracle")
	}
	codec := CursorCodec{Key: []byte("synthetic-native-signing-key-01234567890123456789"), Now: func() time.Time { return time.Unix(1760000000, 0) }}
	for _, c := range cases {
		if got := codec.SignJSON("discovery.activity_deck_cursor", c.Payload); got != c.Token {
			t.Fatal("signed bytes differ from Django")
		}
		for _, token := range []string{c.Token, c.Compressed} {
			var value payload
			if codec.UnsignJSON("discovery.activity_deck_cursor", token, &value, 0) != nil || value != c.Payload {
				t.Fatal("native cannot read Django token")
			}
			if codec.UnsignJSON("other-purpose", token, &value, 0) == nil {
				t.Fatal("salt separation bypass")
			}
			if codec.UnsignJSON("discovery.activity_deck_cursor", token+"x", &value, 0) == nil {
				t.Fatal("tampered signature")
			}
		}
	}
	var decoded payload
	token := codec.SignJSON("discovery.activity_deck_cursor", cases[0].Payload)
	codec.Now = func() time.Time { return time.Unix(1760000002, 0) }
	if codec.UnsignJSON("discovery.activity_deck_cursor", token, &decoded, time.Second) == nil {
		t.Fatal("expired token")
	}
	if codec.UnsignJSON("discovery.activity_deck_cursor", strings.Repeat("x", 25000), &decoded, 0) == nil {
		t.Fatal("token input bound")
	}
}
