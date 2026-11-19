package jsonobj

import "testing"

func TestRoundTripKeepsBytes(t *testing.T) {
	in := `{"sessionId":"local_x","title":"仓库 < CI/CD","n":1795089213563,"nested":{"b":1,"a":[1,2]},"t":true,"z":null}`
	o, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(o.Marshal()); got != in {
		t.Errorf("round trip changed bytes:\n got %s\nwant %s", got, in)
	}
}

func TestSetDeleteKeepOrder(t *testing.T) {
	o, _ := Parse([]byte(`{"a":1,"b":2,"c":3}`))
	o.Set("b", "x")
	o.Delete("a")
	o.Set("d", false)
	if got := string(o.Marshal()); got != `{"b":"x","c":3,"d":false}` {
		t.Errorf("got %s", got)
	}
	if s, _ := o.String("b"); s != "x" {
		t.Errorf("String = %q", s)
	}
}

func TestRejectsNonObject(t *testing.T) {
	for _, in := range []string{`[]`, `"x"`, `{"a":1} {}`, `{"a":}`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("accepted %s", in)
		}
	}
}
