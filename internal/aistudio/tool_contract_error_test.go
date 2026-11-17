package aistudio

import (
	"context"
	"errors"
	"testing"
)

func TestMissingRequiredToolCallError(t *testing.T) {
	source := make(chan Event, 1)
	source <- Event{Kind: EventFinish, FinishReason: "stop"}
	close(source)
	contract := &toolContract{required: true}
	event := <-contract.forward(context.Background(), source)
	var required RequiredToolCallError
	if event.Kind != EventError || !errors.As(event.Err, &required) || required.HTTPStatus() != 400 || required.Error() != EmptyModelOutputMessage {
		t.Fatalf("unexpected required-tool error: %+v", event)
	}
}
