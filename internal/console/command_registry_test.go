package console

import (
	"strings"
	"testing"
)

func TestCommandRegistryParse(t *testing.T) {
	registry := NewCommandRegistry()
	registry.Register("echo", &NetemCommand{
		Desc:  "echo a word",
		Usage: "echo <word>",
		Args:  []string{`^\w+$`},
		Run:   func(*NetemPrompt, []string) {},
	})
	registry.Register("ping", &NetemCommand{
		Desc:  "ping a node",
		Usage: "ping <node>.<if>",
		Args:  []string{`^\w+\.\d+$`},
		Run:   func(*NetemPrompt, []string) {},
	})

	cmd, args, err := registry.Parse([]string{"echo", "hello"})
	if err != nil || cmd == nil || len(args) != 1 || args[0] != "hello" {
		t.Fatalf("Parse = %v %v %v", cmd, args, err)
	}

	if _, _, err := registry.Parse([]string{"missing"}); err == nil || !strings.Contains(err.Error(), "Unknown command") {
		t.Fatalf("unknown command error = %v", err)
	}
	if _, _, err := registry.Parse([]string{"echo"}); err == nil || !strings.Contains(err.Error(), "Wrong number") {
		t.Fatalf("arity error = %v", err)
	}
	if _, _, err := registry.Parse([]string{"echo", "bad!"}); err == nil || !strings.Contains(err.Error(), "Wrong format") {
		t.Fatalf("format error = %v", err)
	}
	if _, _, err := registry.Parse(nil); err == nil {
		t.Fatal("empty command was accepted")
	}

	names := registry.Names()
	if len(names) != 2 || names[0] != "echo" || names[1] != "ping" {
		t.Fatalf("Names = %v, want sorted [echo ping]", names)
	}

	registry.Register("bad", &NetemCommand{Usage: "bad", Args: []string{"["}, Run: func(*NetemPrompt, []string) {}})
	if _, _, err := registry.Parse([]string{"bad", "x"}); err == nil {
		t.Fatal("invalid argument pattern was accepted")
	}
}

func TestCommandRegistryDispatch(t *testing.T) {
	registry := NewCommandRegistry()
	called := false
	registry.Register("do", &NetemCommand{
		Desc:  "test command",
		Usage: "do <word>",
		Args:  []string{`^\w+$`},
		Run: func(_ *NetemPrompt, args []string) {
			called = args[0] == "it"
		},
	})
	prompt := &NetemPrompt{commands: registry}
	prompt.Execute("do it")
	if !called {
		t.Fatal("Execute did not dispatch the command")
	}
}
