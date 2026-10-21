package waa

import (
	"context"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
	"testing"
)

func TestInterpreterCacheKeepsRuntimeStateSeparate(t *testing.T) {
	const source = `globalThis.account = { value: 0 }; account;`
	first, err := interpreterProgram("isolation-test", source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := interpreterProgram("isolation-test", source)
	if err != nil || first != second {
		t.Fatal("compiled program was not reused")
	}
	one, two := goja.New(), goja.New()
	a, err := one.RunProgram(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := two.RunProgram(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ToObject(one).Set("value", 123); err != nil {
		t.Fatal(err)
	}
	if b.ToObject(two).Get("value").ToInteger() != 0 {
		t.Fatal("runtime states were shared")
	}
	third, err := interpreterProgram("isolation-test", source+` account.value = 9;`)
	if err != nil || third == first {
		t.Fatal("changed script reused old bytecode")
	}
}

func TestCachedHostKeepsPromptsSeparate(t *testing.T) {
	options := Options{
		Challenge:   Challenge{GlobalName: "BotGuard", Program: "test", InterpreterHash: "isolation"},
		Interpreter: `var BotGuard={a:function(program,ready){ready(function(callback){callback("!"+document.querySelector("textarea").value);},function(){});}};`,
		Profile:     Profile{PageURL: "https://aistudio.google.com/", Locale: "en-US", TimeZone: "UTC"},
	}
	one, err := NewRuntime(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := NewRuntime(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	for _, item := range []struct {
		vm     *Runtime
		prompt string
	}{{one, "first"}, {two, "second"}, {one, "first-again"}} {
		got, err := item.vm.Proof(context.Background(), "digest", item.prompt)
		if err != nil || got != "!"+item.prompt {
			t.Fatalf("proof state crossed runtimes: %q, %v", got, err)
		}
	}
}
