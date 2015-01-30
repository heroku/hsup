package diag_test

import (
	"fmt"
	"github.com/heroku/hsup/diag"
)

func ExampleLog() {
	diag.Log("世界")
	diag.Log("ascii")
}

func ExampleLogf() {
	diag.Logf("%v %v", "世界", "ascii")
}

func ExampleDiag_Log() {
	dg := diag.New(1024)
	dg.Log("the answer", 42, nil)
	fmt.Println(dg.Contents())

	dg.Log("what is six times nine")
	fmt.Println(dg.Contents())
	// Output:
	// [the answer 42 <nil>]
	// [the answer 42 <nil> what is six times nine]
}

func ExampleDiag_Logf() {
	dg := diag.New(1024)
	dg.Logf("%v %T", "the type of nil is", nil)
	fmt.Println(dg.Contents())
	// Output: [the type of nil is <nil>]
}
