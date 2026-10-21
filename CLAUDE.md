# lets-go-gaffer

Go + RAG assistant for Fantasy Premier League squad building.

## Rules
- Stdlib first. Ask before adding any dependency.
- Small interfaces, defined where they are used.
- Wrap errors with context: fmt.Errorf("doing x: %w", err).
- Every network/DB call takes a context.Context as the first arg.
- Every goroutine needs a clear shutdown path.
- Table-driven tests. Run `go vet ./... && go test -race ./...` before calling anything done.
- Never let the LLM do math or rule checking. Validate squads in plain Go.
- Plan first, then code. Explain non-obvious design choices.