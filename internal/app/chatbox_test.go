package app

import (
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestStripChatboxRuntimeDescription(t *testing.T) {
	for _, description := range []string{chatboxRuntimeDescription, chatboxRuntimeInstruction} {
		original := []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{
			{Text: description}, {Text: "<system-reminder>Current time: today</system-reminder>"},
			{InlineData: &aistudio.Blob{Data: []byte("image")}},
		}}}
		request := aistudio.GenerateRequest{System: "before\n" + description + "\nafter", Contents: original}
		stripChatboxRuntimeDescription(&request)
		if request.System != "before\n\nafter" || len(request.Contents[0].Parts) != 2 {
			t.Fatalf("description not removed: %#v", request)
		}
		if request.Contents[0].Parts[0].Text != original[0].Parts[1].Text || request.Contents[0].Parts[1].InlineData != original[0].Parts[2].InlineData {
			t.Fatal("unrelated reminder or image changed")
		}
		if original[0].Parts[0].Text != description || len(original[0].Parts) != 3 {
			t.Fatal("original input was mutated")
		}
	}
}

func TestStripChatboxBeforeAntiTruncation(t *testing.T) {
	request := aistudio.GenerateRequest{Model: "抗截断/gemini-3.8-flash", System: chatboxRuntimeInstruction,
		Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: chatboxRuntimeDescription + "hello"}}}}}
	stripChatboxRuntimeDescription(&request)
	prepareAntiTruncation(&request)
	if strings.Contains(request.System, "out-of-band") || !strings.Contains(request.System, replyToolInstruction) {
		t.Fatal("cleanup did not preserve anti-truncation instructions")
	}
	if got := string(request.Contents[2].Parts[0].FunctionResult.Content); strings.Contains(got, "out-of-band") || !strings.Contains(got, "hello") {
		t.Fatalf("wrapped input was not cleaned: %s", got)
	}
}

func BenchmarkStripChatboxNoMatch(b *testing.B) {
	text := strings.Repeat("ordinary input ", 65536)
	request := aistudio.GenerateRequest{System: "You are a helpful assistant.", Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: text}}}}}
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		stripChatboxRuntimeDescription(&request)
	}
}
