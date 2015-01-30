package diag

import (
	"reflect"
	"testing"
)

func TestIdentity(t *testing.T) {
	dg := New(10)
	dg.Log("hi")

	result := dg.Contents()
	if !reflect.DeepEqual(result, []string{"hi"}) {
		t.Fatal(result)
	}

	dg.Log("hello")
	result = dg.Contents()
	if !reflect.DeepEqual(result, []string{"hi", "hello"}) {
		t.Fatal(result)
	}

	dg.Log("evict")
	result = dg.Contents()
	if !reflect.DeepEqual(result, []string{"evict"}) {
		t.Fatal(result)
	}
}

func TestTooLong(t *testing.T) {
	dg := New(1)
	dg.Log("hi")
	result := dg.Contents()
	if !reflect.DeepEqual(result, []string(nil)) {
		t.Fatal(result)
	}
}

func TestEmptyBuf(t *testing.T) {
	expect := "Diag requires a zero, non-negative size.  Size specified: 0"
	defer func() {
		if r := recover(); r != expect {
			t.Fatal(r)
		}
	}()

	New(0)
}

func TestEmptyWrite(t *testing.T) {
	dg := New(1)
	dg.Log("")
	if len(dg.Contents()) != 0 {
		t.Fatal("empty records are not representable")
	}
}

func TestTwoArgs(t *testing.T) {
	dg := New(20)
	dg.Log("hello", "world")
	result := dg.Contents()
	if !reflect.DeepEqual(result, []string{"hello world"}) {
		t.Fatal(result)
	}
}

func TestLogf(t *testing.T) {
	dg := New(1024)

	args := [][]interface{}{
		{"%v %v %v", "hello", 1, nil},
		// Too many args.
		{"%v %v %v", "hello", 1, nil, "extra"},
		// Too few.
		{"%v %v", "hello"},
	}

	for _, a := range args {
		dg.Logf(a[0].(string), a[1:]...)
	}

	result := dg.Contents()
	if !reflect.DeepEqual(result, []string{"hello 1 <nil>",
		"hello 1 <nil>%!(EXTRA string=extra)", "hello %!v(MISSING)"}) {
		t.Fatal(result)
	}
}

func TestMultiByte(t *testing.T) {
	dg := New(1)
	dg.Log("世界")
	result := dg.Contents()
	if !reflect.DeepEqual(result, []string(nil)) {
		t.Fatal(result)
	}

	dg = New(100)
	dg.Log("世界")
	dg.Log("日本語")
	dg.Log("ascii")
	result = dg.Contents()
	if !reflect.DeepEqual(result, []string{"世界", "日本語", "ascii"}) {
		t.Fatal(result)
	}
}
