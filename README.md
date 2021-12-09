# Deno "lambda" runner

Simple way to run a lambda-like function using Deno as a sub-process of a Go program.

Functions are written in plain JavaScript and executed using [Deno](https://deno.land/).

At the bare minimum the JavaScript code should define a `handle` function taking an event object (JSON).
Optionally, an `init` function can be defined that is invoked upon cold start with arbitrary configurable (JSON) data.
Debug data can be logged using `console.log` and will be passed to a logging callback.

Most basic function definition returning event data back unmodified and logging the event:

```javascript
function handle(event) {
    console.log("Got event", event);
    return event;
}
```

Example of using the `init` block for cold starts:

```javascript
let token;

function init(cfg) {
	token = cfg.token;
	console.log("Inited");
}

function handle(event) {
	console.log("Invoked with", event);
	return {token: token, name: event.name};
}
```

The `init` will be invoked upon function boot (which happens when creating the function instance with the `NewDenoFunctionInstance` API).
Every invocation (`.Invoke`) will run the `handle` function.

See `deno_test.go` for examples of usage. Run tests with:

    go test