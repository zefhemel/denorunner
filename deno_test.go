package denorunner_test

import (
	"context"
	"denorunner"
	"fmt"
	"testing"
)

func TestDenoBasic(t *testing.T) {
	code := `
function handle(event) {
	console.log("Hello world", event);
	return event;
}
`

	cfg := &denorunner.Config{
		WorkDir:  ".",
		DenoPath: "deno",
	}

	ctx := context.Background()

	fn, err := denorunner.NewDenoFunctionInstance(ctx, cfg, func(message string) {
		fmt.Print(message)
	}, struct{}{}, code)

	if err != nil {
		t.Fatal(err)
	}

	defer fn.Close()

	type data struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}

	resp, err := fn.Invoke(ctx, data{"Pete", "Hello world!"})
	if err != nil {
		t.Fatal(err)
	}

	if obj, ok := resp.(map[string]interface{}); ok {
		if obj["name"] != "Pete" {
			t.Fatal("Invalid return event")
		}
		if obj["message"] != "Hello world!" {
			t.Fatal("Invalid return event")
		}
	} else {
		t.Fatal("Did not get back an event object")
	}
}

func TestDenoWithInit(t *testing.T) {
	code := `
let token;

function init(cfg) {
	token = cfg.token;
	console.log("Inited");
}

function handle(event) {
	console.log("Invoked with", event);
	return {token: token, name: event.name};
}
`

	type initObj struct {
		Token string `json:"token"`
	}

	cfg := &denorunner.Config{
		WorkDir:  ".",
		DenoPath: "deno",
	}

	ctx := context.Background()

	fn, err := denorunner.NewDenoFunctionInstance(ctx, cfg, func(message string) {
		fmt.Print(message)
	}, initObj{"1234"}, code)

	if err != nil {
		t.Fatal(err)
	}

	defer fn.Close()

	type data struct {
		Name    string `json:"name"`
	}

	for i := 0; i < 5; i++ {
		resp, err := fn.Invoke(ctx, data{"Pete"})
		if err != nil {
			t.Fatal(err)
		}

		if obj, ok := resp.(map[string]interface{}); ok {
			if obj["name"] != "Pete" {
				t.Fatal("Invalid return name")
			}
			if obj["token"] != "1234" {
				t.Fatal("Invalid return token")
			}
		} else {
			t.Fatal("Did not get back an event object")
		}
	}
}
