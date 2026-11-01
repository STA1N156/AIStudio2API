package app

import (
	"slices"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

const chatboxRuntimeDescription = "<system-reminder>...</system-reminder> blocks are out-of-band runtime metadata from Chatbox (e.g. the current date and time), not part of the user's message."
const chatboxRuntimeInstruction = chatboxRuntimeDescription + " Treat their contents as authoritative; do not quote or acknowledge them."

func stripChatboxText(text string) string {
	if !strings.Contains(text, "out-of-band runtime metadata from Chatbox") {
		return text
	}
	return strings.ReplaceAll(strings.ReplaceAll(text, chatboxRuntimeInstruction, ""), chatboxRuntimeDescription, "")
}

// stripChatboxRuntimeDescription runs before input wrapping, never on response chunks.
// Only matching text is copied; tool arguments, media and signed thoughts stay intact.
func stripChatboxRuntimeDescription(request *aistudio.GenerateRequest) {
	request.System = stripChatboxText(request.System)
	copied := false
	for index, content := range request.Contents {
		var cleaned []aistudio.Part
		for partIndex, part := range content.Parts {
			text := part.Text
			if !part.Thought {
				text = stripChatboxText(text)
			}
			if text != part.Text {
				if cleaned == nil {
					cleaned = append(make([]aistudio.Part, 0, len(content.Parts)), content.Parts[:partIndex]...)
				}
				textOnly := part == (aistudio.Part{Text: part.Text, ThoughtSignature: part.ThoughtSignature})
				if text != "" || !textOnly {
					part.Text = text
					cleaned = append(cleaned, part)
				}
			} else if cleaned != nil {
				cleaned = append(cleaned, part)
			}
		}
		if cleaned != nil {
			if !copied {
				request.Contents = slices.Clone(request.Contents)
				copied = true
			}
			request.Contents[index].Parts = cleaned
		}
	}
}
