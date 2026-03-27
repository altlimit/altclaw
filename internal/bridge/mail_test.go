package bridge

import (
	"testing"

	"github.com/dop251/goja"
)

func setupMailVM(t *testing.T) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	RegisterMail(vm, nil)
	return vm
}

func TestMail_Registration(t *testing.T) {
	vm := setupMailVM(t)
	val, err := vm.RunString(`typeof mail`)
	if err != nil {
		t.Fatalf("typeof mail failed: %v", err)
	}
	if val.String() != "object" {
		t.Errorf("mail should be an object, got %q", val.String())
	}
}

func TestMail_SendRegistered(t *testing.T) {
	vm := setupMailVM(t)
	val, err := vm.RunString(`typeof mail.send`)
	if err != nil {
		t.Fatalf("typeof mail.send failed: %v", err)
	}
	if val.String() != "function" {
		t.Errorf("mail.send should be a function, got %q", val.String())
	}
}

func TestMail_ConnectRegistered(t *testing.T) {
	vm := setupMailVM(t)
	val, err := vm.RunString(`typeof mail.connect`)
	if err != nil {
		t.Fatalf("typeof mail.connect failed: %v", err)
	}
	if val.String() != "function" {
		t.Errorf("mail.connect should be a function, got %q", val.String())
	}
}

func TestMailSend_MissingArgs(t *testing.T) {
	vm := setupMailVM(t)
	_, err := vm.RunString(`mail.send()`)
	if err == nil {
		t.Fatal("expected error for mail.send() with no arguments")
	}
}

func TestMailSend_MissingHost(t *testing.T) {
	vm := setupMailVM(t)
	_, err := vm.RunString(`mail.send({from: "a@b.com", to: ["c@d.com"], subject: "hi", body: "yo"})`)
	if err == nil {
		t.Fatal("expected error for mail.send() without host")
	}
}

func TestMailSend_MissingTo(t *testing.T) {
	vm := setupMailVM(t)
	_, err := vm.RunString(`mail.send({host: "smtp.example.com", from: "a@b.com", subject: "hi", body: "yo"})`)
	if err == nil {
		t.Fatal("expected error for mail.send() without to")
	}
}

func TestMailConnect_MissingArgs(t *testing.T) {
	vm := setupMailVM(t)
	_, err := vm.RunString(`mail.connect()`)
	if err == nil {
		t.Fatal("expected error for mail.connect() with no arguments")
	}
}

func TestMailConnect_MissingHost(t *testing.T) {
	vm := setupMailVM(t)
	_, err := vm.RunString(`mail.connect({user: "me", pass: "secret"})`)
	if err == nil {
		t.Fatal("expected error for mail.connect() without host")
	}
}
