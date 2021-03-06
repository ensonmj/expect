package expect

import (
	"io"
	"testing"
)

func TestHelloWorld(t *testing.T) {
	child, err := Spawn("echo \"Hello World\"")
	if err != nil {
		t.Fatal(err)
	}
	err = child.Expect("Hello World")
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
}

func TestReadLine(t *testing.T) {
	child, err := Spawn("echo \"foo\nbar\"")
	if err != nil {
		t.Fatal(err)
	}

	type Test struct {
		data string
	}
	var tests = []Test{
		// terminal user "\r\n" as line seperator for output
		{"foo\r\n"},
		{"bar\r\n"},
	}
	for _, test := range tests {
		str, err := child.ReadLine()
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		if str != test.data {
			t.Fatalf("Expected %v, but got %v", []byte(test.data), []byte(str))
		} else {
			t.Logf("Expected %v", test.data)
		}
	}
}

func TestExpect(t *testing.T) {
	child, err := Spawn("echo \"expect$tail\"")
	if err != nil {
		t.Fatal(err)
	}
	child.Expect("$")
	str, err := child.ReadLine()
	if str != "tail\r\n" {
		t.Fatalf("Expected %v, but got %v", "tail", str)
	} else {
		t.Logf("Expected %v", "tail")
	}
}

func TestExpectMulti(t *testing.T) {
	child, err := Spawn("echo \"expect$tail\"")
	if err != nil {
		t.Fatal(err)
	}

	output := "fail"
	pairs := []ExpectPair{
		{"expect", func(_ []byte) error {
			output = "success"
			return nil
		}},
		{"$", nil},
	}
	child.ExpectMulti(pairs)
	if output != "success" {
		t.Errorf("Expected 'success', but got '%v'", output)
	} else {
		t.Log("Expected 'success'")
	}

	str, err := child.ReadLine()
	if str != "$tail\r\n" {
		t.Fatalf("Expected 'tail', but got '%v'", str)
	} else {
		t.Log("Expected 'tail'")
	}
}
